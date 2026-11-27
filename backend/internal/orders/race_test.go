package orders

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"kacheem/backend/internal/pgtest"
	"kacheem/backend/internal/postgres"
)

// widePool opens a second pool on the same test database with enough
// connections that the buyers really do run concurrently inside PostgreSQL.
func widePool(t *testing.T, pool *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	wide, err := postgres.Open(ctx, pool.Config().ConnString()+"&pool_max_conns=40")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wide.Close)
	return wide
}

// race runs fn for n buyers released at the same instant.
func race(n int, fn func(i int)) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := range n {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			fn(i)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}

// Release test "Capacity and money" (plan section 10): 100 concurrent
// one-ticket buyers for the last 10 places; exactly 10 holds succeed.
func TestHundredBuyersRaceForLastTenPlaces(t *testing.T) {
	pool := setup(t, pgtest.Event{Shards: []int32{4, 3, 2, 1}})
	wide := widePool(t, pool)

	var mu sync.Mutex
	won, soldOut := 0, 0
	var unexpected []error
	race(100, func(i int) {
		_, _, err := Reserve(ctx, wide, request(fmt.Sprintf("buyer-%03d", i), "key-00000001", 1))
		var so *SoldOutError
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			won++
		case errors.As(err, &so) && so.Remaining == 0:
			soldOut++
		default:
			unexpected = append(unexpected, err)
		}
	})
	if won != 10 || soldOut != 90 || len(unexpected) > 0 {
		t.Fatalf("won %d, sold out %d, unexpected %v", won, soldOut, unexpected)
	}
	if held(t, pool) != 10 {
		t.Fatalf("held = %d", held(t, pool))
	}
	assertInvariants(t, pool)
}

// Mixed quantities across 16 shards never oversell, and every refusal
// reports a remaining count that really was too small at that moment.
func TestMixedQuantitiesNeverOversell(t *testing.T) {
	shards := make([]int32, 16)
	for i := range shards {
		shards[i] = 3
	}
	pool := setup(t, pgtest.Event{Shards: shards}) // 48 places
	wide := widePool(t, pool)

	qty := make([]int, 60)
	for i := range qty {
		qty[i] = 1 + rand.IntN(4)
	}
	var mu sync.Mutex
	granted := 0
	var unexpected []error
	race(len(qty), func(i int) {
		_, _, err := Reserve(ctx, wide, request(fmt.Sprintf("buyer-%03d", i), "key-00000001", qty[i]))
		var so *SoldOutError
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			granted += qty[i]
		case errors.As(err, &so) && so.Remaining < qty[i]:
		default:
			unexpected = append(unexpected, err)
		}
	})
	if len(unexpected) > 0 {
		t.Fatalf("unexpected errors: %v", unexpected)
	}
	if granted > 48 || held(t, pool) != granted {
		t.Fatalf("granted %d, held %d, capacity 48", granted, held(t, pool))
	}
	assertInvariants(t, pool)
}

// Cancel, expiry and repeated expiry racing on the same hold release it once.
func TestReleaseRacesReleaseExactlyOnce(t *testing.T) {
	pool := setup(t, pgtest.Event{})
	wide := widePool(t, pool)
	for round := range 5 {
		buyer := fmt.Sprintf("buyer-%d", round)
		o, _, err := Reserve(ctx, pool, request(buyer, "key-00000001", 3))
		if err != nil {
			t.Fatal(err)
		}
		expire(t, pool, o.Ref)
		var mu sync.Mutex
		released := 0
		race(20, func(i int) {
			var ok bool
			if i%2 == 0 {
				ok = Cancel(ctx, wide, buyer, o.Ref) == nil
			} else {
				n, _ := ExpireDue(ctx, wide, 10)
				ok = n == 1
			}
			if ok {
				mu.Lock()
				released++
				mu.Unlock()
			}
		})
		if released != 1 {
			t.Fatalf("round %d: released %d times", round, released)
		}
		assertInvariants(t, pool)
	}
	if held(t, pool) != 0 {
		t.Fatalf("held = %d", held(t, pool))
	}
}
