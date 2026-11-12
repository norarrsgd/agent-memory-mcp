package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"os"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkWorkloads(b *testing.B) {
	for _, size := range []int{10000, 50000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			svc, store := setup(b)
			ctx := context.Background()
			snapshot := domain.Export{FormatVersion: 1, ExportedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			ts := "2026-11-12T00:00:00.000000000Z"
			for i := 0; i < size; i++ {
				project := "alpha"
				if i%2 != 0 {
					project = "beta"
				}
				scope := "project"
				if i%10 == 0 {
					project = ""
					scope = "global"
				}
				topic := fmt.Sprintf("topic%d", i%100)
				snapshot.Memories = append(snapshot.Memories, domain.Memory{ID: uuid(), Scope: scope, ProjectKey: project, Type: "fact", Title: topic + " migration constraint", Content: topic + " transactional migration preflight validation architecture and recovery lessons", Tags: []string{"database"}, SourceAgent: "benchmark", CreatedAt: ts, UpdatedAt: ts, Version: 1, Status: "active", Evidence: "observation"})
			}
			if err := store.Import(ctx, snapshot); err != nil {
				b.Fatal(err)
			}
			firstID := snapshot.Memories[1].ID
			status := func(b *testing.B) {
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				b.ReportMetric(float64(mem.HeapAlloc), "heap_B")
				if f, err := os.Stat(store.Path); err == nil {
					b.ReportMetric(float64(f.Size()), "db_B")
				}
				if f, err := os.Stat(store.Path + "-wal"); err == nil {
					b.ReportMetric(float64(f.Size()), "wal_B")
				}
			}
			b.Run("Insert", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := svc.Upsert(ctx, input()); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				status(b)
			})
			for _, project := range []string{"", "alpha"} {
				name := "SearchGlobal"
				if project != "" {
					name = "SearchProject"
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					latencies := make([]int64, 0, b.N)
					var payload int
					for i := 0; i < b.N; i++ {
						start := time.Now()
						query := "topic42 migration"
						if project == "" {
							query = "topic40 migration"
						}
						r, err := svc.Search(ctx, domain.SearchInput{Query: query, ProjectKey: project})
						latencies = append(latencies, time.Since(start).Nanoseconds())
						if err != nil {
							b.Fatal(err)
						}
						data, _ := json.Marshal(r)
						payload = len(data)
					}
					b.StopTimer()
					sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
					if len(latencies) > 0 {
						b.ReportMetric(float64(latencies[len(latencies)/2]), "p50-ns")
						b.ReportMetric(float64(latencies[(len(latencies)-1)*95/100]), "p95-ns")
					}
					b.ReportMetric(float64(payload), "payload_B")
					status(b)
				})
			}
			b.Run("Get", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := svc.Get(ctx, domain.GetInput{ID: firstID}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("ConcurrentReads", func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if _, err := svc.Get(ctx, domain.GetInput{ID: firstID}); err != nil {
							b.Error(err)
						}
					}
				})
			})
			b.Run("Mixed", func(b *testing.B) {
				b.ReportAllocs()
				var sequence atomic.Uint64
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if sequence.Add(1)%10 == 0 {
							if _, err := svc.Upsert(ctx, input()); err != nil {
								b.Error(err)
							}
						} else {
							if _, err := svc.Search(ctx, domain.SearchInput{Query: "topic42 migration", ProjectKey: "alpha"}); err != nil {
								b.Error(err)
							}
						}
					}
				})
			})
		})
	}
}
