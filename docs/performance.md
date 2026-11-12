# Measured performance

Measured on macOS ARM64, Apple M4 Pro, Go 1.27.4, with pure-Go SQLite v1.62.0, WAL, and FULL synchronous mode. These are synthetic local measurements, not service-level guarantees or comparisons against other drivers.

Command (module dependencies were already cached):

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test ./internal/memory -run '^$' -bench BenchmarkWorkloads -benchtime=100x -benchmem
```

Each dataset has 10,000 or 50,000 initial records, two projects, 10% global records, 100 recurring topics, short technical text, and one tag. Seeding uses a single import transaction; insertion benchmarks measure individual validated transactions afterward. Each subbenchmark executes 100 measured operations. Search returns five results: global uses `topic40 migration`, project-plus-global uses `topic42 migration`. Tests use real private temporary databases; no LLM or network service participates.

| Initial records | Insert average | Global search p95 | Project search p95 | Get average |
| --- | --- | --- | --- | --- |
| 10,000 | 255 µs | 0.733 ms | 0.779 ms | 8.43 µs |
| 50,000 | 233 µs | 3.043 ms | 2.932 ms | 8.94 µs |

Concurrent retrieval averaged 7.72/11.07 µs per operation at 10,000/50,000 records. A parallel 90%-search/10%-insert workload averaged 0.349/1.130 ms per operation; these throughput-derived averages are not individual request latency percentiles. Concurrency tests separately verify version conflicts and independent process access.

The five-result JSON payload was 1,363 bytes globally and 1,453 bytes for project search. These sizes measure the logical response, before MCP adds its text mirror, result wrapper, and JSON-RPC envelope; production budgets include both result representations.

Observed database/WAL allocated sizes during the runs were 11,264,000/11,325,912 bytes at 10,000 records and 55,775,232/56,097,952 bytes at 50,000. The WAL file retains reusable allocated space after checkpointing, and bulk seeding creates a large initial WAL; its file size is not the amount of uncheckpointed data. Sizes include benchmark writes beyond the initial dataset.

Sampled Go heap allocation was approximately 1.3–2.0 MB during insertion/search benchmarks; allocations per search were about 41–45 KB. This excludes mmap/native storage and is not total process memory. `/usr/bin/time -l` around the benchmark command reported maximum resident set size 168,001,536 bytes and peak memory footprint 28,213,944 bytes for the benchmark command/process tree, including tooling and dataset preparation. These are not idle server measurements. The CGO-disabled executable is approximately 17 MB with default symbol/debug information.

## Query plans

The inspected project-plus-global FTS query used:

```text
SCAN memories_fts VIRTUAL TABLE INDEX 0:M3
SEARCH m USING INTEGER PRIMARY KEY (rowid=?)
USE TEMP B-TREE FOR ORDER BY
```

The virtual-table scan is FTS MATCH retrieval, followed by row-ID lookups and scope/status filters, not a full ordinary-memory-table scan. Composite project/status/type indexes, an active-handoff index, and tag indexes support non-FTS discovery and filters. Ranking/tie sorting uses a temporary B-tree over matching candidates.

Broad/common lexical queries can require evaluating many FTS candidates before scope filtering and sorting. Current measurements do not justify a second indexing system or semantic search. Re-measure with representative private workloads, longer bodies, skewed projects, and larger query fan-out before optimizing. No claim about semantic relevance, token savings, idle RSS, or cross-device performance is made.
