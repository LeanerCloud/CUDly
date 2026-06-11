// Standalone module: the E2E suite uses only the standard library so the
// test-runner image (Dockerfile.test) builds without downloading the main
// module's dependency tree. Excluded from the root module on purpose.
module github.com/LeanerCloud/CUDly/tests/e2e

go 1.25
