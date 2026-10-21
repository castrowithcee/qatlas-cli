package config

// Names the external test package (worker_main_test.go, storage_bench_test.go) needs from the internal
// tests. It is external because it imports the selftest package, which imports this one.
var TestProviders = testProviders

const (
	WorkerModeEnv = workerModeEnv
	WorkerPathEnv = workerPathEnv
	WorkerNameEnv = workerNameEnv
)
