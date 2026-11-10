// Package autoflowtest serves a mocked AutoFlow service, with or without TLS,
// for the tests of the `glab autoflow workflow` commands.
package autoflowtest

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -typed -destination=./autoflow_mocks.go -package=autoflowtest gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1 AutoFlowServiceServer
