package orders

import (
	"crypto/rand"
	"slices"
)

type shardFree struct {
	Shard int16 `db:"shard"`
	Free  int   `db:"free"`
}

type part struct {
	Shard int16
	Qty   int
}

// allocate takes qty places from shards (listed in ascending shard order),
// visiting them in rotation from index start so concurrent holds spread out.
// It returns the parts in ascending shard order, or nil if qty does not fit.
func allocate(shards []shardFree, qty, start int) []part {
	var parts []part
	need := qty
	for i := range shards {
		if need == 0 {
			break
		}
		s := shards[(start+i)%len(shards)]
		take := min(s.Free, need)
		if take > 0 {
			parts = append(parts, part{s.Shard, take})
			need -= take
		}
	}
	if need > 0 {
		return nil
	}
	slices.SortFunc(parts, func(a, b part) int { return int(a.Shard) - int(b.Shard) })
	return parts
}

func totalFree(shards []shardFree) int {
	n := 0
	for _, s := range shards {
		n += s.Free
	}
	return n
}

// Crockford Base32 without I, L, O and U, so references survive being read aloud.
const refAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newRef returns a random public order reference such as "7K2QH-M9X4D".
// 50 random bits; 256 is a multiple of 32, so b%32 is unbiased.
func newRef() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	out := make([]byte, 0, 11)
	for i, c := range b {
		if i == 5 {
			out = append(out, '-')
		}
		out = append(out, refAlphabet[c%32])
	}
	return string(out)
}
