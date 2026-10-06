// Package ingestion owns source synchronization attempts and retryable
// discovery, fetch, normalize and index jobs. URL ingestion is synchronous for
// the initial slice and can move behind the worker without changing its ports.
package ingestion
