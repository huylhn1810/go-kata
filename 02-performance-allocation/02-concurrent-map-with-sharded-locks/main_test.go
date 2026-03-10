package main

import (
	"runtime"
	"sync"
	"testing"
)

// -----------------------------------------------------------------
// Self-Correction 1 – The Contention Test
//
// Run 8 goroutines doing only Set() with sequential keys.
// With  1 shard  → heavy contention (benchmark shows low throughput).
// With 16 shards → near-linear scaling (benchmark shows high throughput).
//
// The subtest verifies functional correctness of concurrent Set()
// under both configurations; use:
//
//	go test -bench=BenchmarkContention -benchtime=3s
//
// to compare raw throughput numbers side-by-side.
// -----------------------------------------------------------------

func TestContentionTest(t *testing.T) {
	t.Run("concurrent_set_1_shard_vs_16_shards", func(t *testing.T) {
		const goroutines = 8
		const keysPerGoroutine = 1_000

		runConcurrentSet := func(shardCount int) {
			sm := NewShardedMap[int, int](shardCount)
			var wg sync.WaitGroup
			wg.Add(goroutines)

			for g := 0; g < goroutines; g++ {
				g := g
				go func() {
					defer wg.Done()
					base := g * keysPerGoroutine
					for i := 0; i < keysPerGoroutine; i++ {
						sm.Set(base+i, base+i)
					}
				}()
			}
			wg.Wait()

			// Verify every key is present and has the correct value.
			total := goroutines * keysPerGoroutine
			for i := 0; i < total; i++ {
				v, ok := sm.Get(i)
				if !ok {
					t.Errorf("shard=%d: key %d missing", shardCount, i)
				}
				if v != i {
					t.Errorf("shard=%d: key %d = %d, want %d", shardCount, i, v, i)
				}
			}
		}

		t.Log("running with 1 shard (expect higher contention)…")
		runConcurrentSet(1)

		t.Log("running with 16 shards (expect near-linear scaling)…")
		runConcurrentSet(16)
	})
}

// BenchmarkContention compares throughput between 1 shard and 16 shards.
// Run with:
//
//	go test -bench=BenchmarkContention -benchtime=3s
//
// The output will show two clearly-labelled lines, e.g.:
//
//	BenchmarkContention/1_shard-8     ... ns/op
//	BenchmarkContention/16_shards-8   ... ns/op
func BenchmarkContention(b *testing.B) {
	cases := []struct {
		name   string
		shards int
	}{
		{"1_shard", 1},
		{"16_shards", 16},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			sm := NewShardedMap[int, int](tc.shards)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					sm.Set(i, i)
					i++
				}
			})
		})
	}
}

// -----------------------------------------------------------------
// Self-Correction 2 – The Memory Test
//
// Store 1 million int keys with int values and verify the heap
// growth stays below 50 MB compared to a baseline empty map.
//
// Fail condition (from README):
//   > If your solution uses more than 50MB extra memory vs baseline map.
//
// Hint: avoid string(key) conversions; use type-safe hashing.
// -----------------------------------------------------------------

func TestMemoryTest(t *testing.T) {
	t.Run("1_million_int_keys_under_50mb_overhead", func(t *testing.T) {
		const keys = 1_000_000
		const maxExtraBytes = 50 * 1024 * 1024 // 50 MB

		// Baseline: measure heap after GC with no map.
		runtime.GC()
		var baseStats runtime.MemStats
		runtime.ReadMemStats(&baseStats)

		sm := NewShardedMap[int, int](64)
		for i := 0; i < keys; i++ {
			sm.Set(i, i)
		}

		// Force GC so we count live allocations only.
		runtime.GC()
		var afterStats runtime.MemStats
		runtime.ReadMemStats(&afterStats)

		var extraBytes uint64
		if afterStats.HeapAlloc > baseStats.HeapAlloc {
			extraBytes = afterStats.HeapAlloc - baseStats.HeapAlloc
		}

		t.Logf("baseline heap: %d B | after insert: %d B | extra: %d B (limit %d B)",
			baseStats.HeapAlloc, afterStats.HeapAlloc, extraBytes, maxExtraBytes)

		if extraBytes > maxExtraBytes {
			t.Errorf("memory overhead %d B exceeds 50 MB limit (%d B)",
				extraBytes, maxExtraBytes)
		}

		// Sanity-check: spot-check 100 random-ish keys.
		for i := 0; i < keys; i += keys / 100 {
			v, ok := sm.Get(i)
			if !ok {
				t.Errorf("key %d missing after insert", i)
			}
			if v != i {
				t.Errorf("key %d = %d, want %d", i, v, i)
			}
		}
	})
}

// -----------------------------------------------------------------
// Self-Correction 3 – The Race Test
//
// Run concurrent Get / Set / Delete operations and verify there are
// no data races.  Execute with:
//
//	go test -race -run TestRaceTest
//
// Any detected data race = automatic failure.
// The test is bounded and will always finish; it does NOT rely on
// timeouts.  If the implementation deadlocks the test will hang,
// which is a clear signal of a locking bug.
// -----------------------------------------------------------------

func TestRaceTest(t *testing.T) {
	t.Run("concurrent_read_write_delete_no_race", func(t *testing.T) {
		const shards = 16
		const workers = 20
		const opsPerWorker = 500

		sm := NewShardedMap[int, int](shards)

		// Pre-populate so Delete and Get have something to work with.
		for i := 0; i < workers*opsPerWorker; i++ {
			sm.Set(i, i)
		}

		// quit signals the background Keys() goroutine to stop.
		quit := make(chan struct{})

		// keysWg tracks the background goroutine separately so we can
		// wait for it after all workers are done – no deadlock possible.
		var keysWg sync.WaitGroup
		keysWg.Add(1)
		go func() {
			defer keysWg.Done()
			for {
				select {
				case <-quit:
					return
				default:
					_ = sm.Keys()
				}
			}
		}()

		var wg sync.WaitGroup
		wg.Add(workers * 3) // writers + readers + deleters

		// Writers
		for w := 0; w < workers; w++ {
			w := w
			go func() {
				defer wg.Done()
				base := w * opsPerWorker
				for i := 0; i < opsPerWorker; i++ {
					sm.Set(base+i, base+i)
				}
			}()
		}

		// Readers
		for w := 0; w < workers; w++ {
			w := w
			go func() {
				defer wg.Done()
				base := w * opsPerWorker
				for i := 0; i < opsPerWorker; i++ {
					sm.Get(base + i)
				}
			}()
		}

		// Deleters
		for w := 0; w < workers; w++ {
			w := w
			go func() {
				defer wg.Done()
				base := w * opsPerWorker
				for i := 0; i < opsPerWorker; i++ {
					sm.Delete(base + i)
				}
			}()
		}

		// Wait for all workers to finish, then stop the Keys() goroutine.
		wg.Wait()
		close(quit)
		keysWg.Wait()
	})
}
