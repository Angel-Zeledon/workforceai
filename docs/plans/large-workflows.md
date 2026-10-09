# Large workflows

## W1 - Scheduler throughput

- **Per-project parallelism.** `projects` passes its `max_parallel` (1..64, default 4) to `SubmitPlan` through `application.WithRunParallel(ctx, n)`; `Orchestrator.execute` uses it instead of `MAX_PARALLEL` for that request. It stays bounded by the per-organization queue (`MAX_PARALLEL_PER_ORG`, default 8, unchanged): a project with `max_parallel=16` only reaches 16 concurrent runtime calls if the org cap is raised (env). Not persisted in the run meta: a request resumed by restart recovery uses `MAX_PARALLEL`.
- **Waiting tasks release their slot.** `application.YieldSlot(ctx)` gives the scheduler slot back while a task waits for a human or a timer (project gate hold, gate approval, tool approval, guard pause/kill switch, budget-cap pause) and takes one again before the task continues. Resuming tasks are served before new ready nodes; on cancel they are granted regardless of the limit so everything winds down. The org-queue slot was already only held during a runtime call (`orgqueue.go`), so waits never held it.
- **Ready queue.** Ready nodes are launched by longest remaining downstream path (`Node.Weight`, default 1 per node; ties keep list order). Indegree counters and a dependents index make completion O(dependents); the scheduler no longer rescans the node list.
- **Not done:** per-agent concurrency cap (AGENT_MAX_CONCURRENT).
