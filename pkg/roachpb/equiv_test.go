package roachpb

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/stretchr/testify/require"
)

func (l Lease_24_1) Type() LeaseType {
	if l.Epoch == 0 {
		return LeaseExpiration
	}
	return LeaseEpoch
}

func (l Lease_24_1) GetExpiration() hlc.Timestamp {
	if l.Expiration == nil {
		return hlc.Timestamp{}
	}
	return *l.Expiration
}

func (l Lease_24_1) Equivalent(newL Lease_24_1, expToEpochEquiv bool) bool {
	// Ignore proposed timestamp & deprecated start stasis.
	l.ProposedTS, newL.ProposedTS = nil, nil
	l.DeprecatedStartStasis, newL.DeprecatedStartStasis = nil, nil
	// Ignore sequence numbers, they are simply a reflection of
	// the equivalency of other fields.
	l.Sequence, newL.Sequence = 0, 0
	// Ignore the acquisition type, as leases will always be extended via
	// RequestLease requests regardless of how a leaseholder first acquired its
	// lease.
	l.AcquisitionType, newL.AcquisitionType = 0, 0
	// Ignore the ReplicaDescriptor's type. This shouldn't affect lease
	// equivalency because Raft state shouldn't be factored into the state of a
	// Replica's lease. We don't expect a leaseholder to ever become a LEARNER
	// replica, but that also shouldn't prevent it from extending its lease.
	l.Replica.Type, newL.Replica.Type = 0, 0
	// If both leases are epoch-based, we must dereference the epochs
	// and then set to nil.
	switch l.Type() {
	case LeaseEpoch:
		// Ignore expirations. This seems benign but since we changed the
		// nullability of this field in the 1.2 cycle, it's crucial and
		// tested in TestLeaseEquivalence.
		l.Expiration, newL.Expiration = nil, nil

		if l.Epoch == newL.Epoch {
			l.Epoch, newL.Epoch = 0, 0
		}
	case LeaseExpiration:
		switch newL.Type() {
		case LeaseEpoch:
			// An expiration-based lease being promoted to an epoch-based lease. This
			// transition occurs after a successful lease transfer if the setting
			// kv.transfer_expiration_leases_first.enabled is enabled.
			//
			// Expiration-based leases carry a local expiration timestamp. Epoch-based
			// leases store their expiration indirectly in NodeLiveness. We assume that
			// this promotion is only proposed if the liveness expiration is later than
			// previous expiration carried by the expiration-based lease. This is a
			// case where Equivalent is not commutative, as the reverse transition
			// (from epoch-based to expiration-based) requires a sequence increment.
			//
			// Ignore epoch and expiration. The remaining fields which are compared
			// are Replica and Start.
			if expToEpochEquiv {
				l.Epoch, newL.Epoch = 0, 0
				l.Expiration, newL.Expiration = nil, nil
			}

		case LeaseExpiration:
			// See the comment above, though this field's nullability wasn't
			// changed. We nil it out for completeness only.
			l.Epoch, newL.Epoch = 0, 0

			// For expiration-based leases, extensions are considered equivalent.
			// This is one case where Equivalent is not commutative and, as such,
			// requires special handling beneath Raft (see checkForcedErr).
			if l.GetExpiration().LessEq(newL.GetExpiration()) {
				l.Expiration, newL.Expiration = nil, nil
			}
		}
	}
	return l == newL
}

func (l Lease) Equivalent_v24_2(newL Lease, expToEpochEquiv bool) bool {
	return l.Equivalent(newL, expToEpochEquiv)
}

type validLease Lease

func (validLease) generate(r *rand.Rand, keySize int) validLease {
	var exp *hlc.Timestamp
	var epoch int64
	if r.Intn(2) == 0 {
		// Expiration-based lease.
		exp = &hlc.Timestamp{WallTime: r.Int63n(3), Logical: r.Int31n(2)}
	} else {
		// Epoch-based lease.
		epoch = r.Int63n(3)
	}
	return validLease{
		Start:      hlc.ClockTimestamp{WallTime: r.Int63n(3), Logical: r.Int31n(2)},
		Expiration: exp,
		Replica: ReplicaDescriptor{
			NodeID:    NodeID(r.Int31n(2)),
			StoreID:   StoreID(r.Int31n(2)),
			ReplicaID: ReplicaID(r.Int31n(2)),
			Type:      ReplicaType(r.Intn(7)),
		},
		DeprecatedStartStasis: &hlc.Timestamp{WallTime: r.Int63n(3), Logical: r.Int31n(2)},
		ProposedTS:            hlc.ClockTimestamp{WallTime: r.Int63n(3), Logical: r.Int31n(2)},
		Epoch:                 epoch,
		AcquisitionType:       LeaseAcquisitionType(r.Intn(2) + 1),
	}
}

func (l validLease) asLease_v24_1() Lease_24_1 {
	l2 := Lease(l)
	data, err := l2.Marshal()
	if err != nil {
		panic(err)
	}
	var l1 Lease_24_1
	if err := l1.Unmarshal(data); err != nil {
		panic(err)
	}
	return l1
}

func (l validLease) asLease_v24_2() Lease {
	return Lease(l)
}

// Generate implements quick.Generator.
func (validLease) Generate(r *rand.Rand, size int) reflect.Value {
	return reflect.ValueOf(validLease{}.generate(r, size))
}

func TestEquivalentBetweenVersions(t *testing.T) {
	equivalent_v24_1 := func(a, b validLease, expToEpochEquiv bool) bool {
		return a.asLease_v24_1().Equivalent(b.asLease_v24_1(), expToEpochEquiv)
	}
	equivalent_v24_2 := func(a, b validLease, expToEpochEquiv bool) bool {
		return a.asLease_v24_2().Equivalent(b.asLease_v24_2(), expToEpochEquiv)
	}
	require.NoError(t, quick.CheckEqual(equivalent_v24_1, equivalent_v24_2, nil))
}
