-- name: CreateEvaluationTestSet :exec
INSERT INTO evaluation_test_sets (workspace_id, set_id, name, created_at, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(set_id), sqlc.arg(name), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: ListEvaluationTestSets :many
SELECT workspace_id, set_id, name, created_at, updated_at FROM evaluation_test_sets
WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY updated_at DESC, set_id;

-- name: GetEvaluationTestSet :one
SELECT workspace_id, set_id, name, created_at, updated_at FROM evaluation_test_sets
WHERE workspace_id = sqlc.arg(workspace_id) AND set_id = sqlc.arg(set_id);

-- name: UpdateEvaluationTestSet :execrows
UPDATE evaluation_test_sets SET name = sqlc.arg(name), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND set_id = sqlc.arg(set_id);

-- name: DeleteEvaluationTestSet :execrows
DELETE FROM evaluation_test_sets WHERE workspace_id = sqlc.arg(workspace_id) AND set_id = sqlc.arg(set_id);

-- name: CreateEvaluationTestCase :exec
INSERT INTO evaluation_test_cases (workspace_id, case_id, set_id, question, expected_source_id, note, created_at, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(case_id), sqlc.arg(set_id), sqlc.arg(question), sqlc.arg(expected_source_id), sqlc.arg(note), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: ListEvaluationTestCases :many
SELECT workspace_id, case_id, set_id, question, expected_source_id, note, created_at, updated_at
FROM evaluation_test_cases WHERE workspace_id = sqlc.arg(workspace_id) AND set_id = sqlc.arg(set_id)
ORDER BY created_at, case_id;

-- name: UpdateEvaluationTestCase :execrows
UPDATE evaluation_test_cases SET question = sqlc.arg(question), expected_source_id = sqlc.arg(expected_source_id), note = sqlc.arg(note), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND set_id = sqlc.arg(set_id) AND case_id = sqlc.arg(case_id);

-- name: DeleteEvaluationTestCase :execrows
DELETE FROM evaluation_test_cases WHERE workspace_id = sqlc.arg(workspace_id) AND set_id = sqlc.arg(set_id) AND case_id = sqlc.arg(case_id);

-- name: CreateEvaluationRun :exec
INSERT INTO evaluation_runs (workspace_id, run_id, set_id, set_name, top_k, threshold, total_count, evaluable_count, passed_count, source_missing_count, pass_rate, results, created_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(run_id), sqlc.arg(set_id), sqlc.arg(set_name), sqlc.arg(top_k), sqlc.arg(threshold), sqlc.arg(total_count), sqlc.arg(evaluable_count), sqlc.arg(passed_count), sqlc.arg(source_missing_count), sqlc.arg(pass_rate), sqlc.arg(results), sqlc.arg(created_at));

-- name: ListEvaluationRuns :many
SELECT workspace_id, run_id, set_id, set_name, top_k, threshold, total_count, evaluable_count, passed_count, source_missing_count, pass_rate, results, created_at
FROM evaluation_runs WHERE workspace_id = sqlc.arg(workspace_id) AND set_id = sqlc.arg(set_id)
ORDER BY created_at DESC, run_id DESC;

-- name: GetEvaluationRun :one
SELECT workspace_id, run_id, set_id, set_name, top_k, threshold, total_count, evaluable_count, passed_count, source_missing_count, pass_rate, results, created_at
FROM evaluation_runs WHERE workspace_id = sqlc.arg(workspace_id) AND run_id = sqlc.arg(run_id);
