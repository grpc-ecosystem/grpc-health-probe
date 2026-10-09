// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package e2e holds end-to-end tests that build the grpc-health-probe binary
// and run it against an in-process gRPC server.
//
// To measure how much of the probe these tests exercise, build it with
// coverage instrumentation by setting GOCOVERDIR and report on the collected
// data afterwards:
//
//	export GOCOVERDIR=$(mktemp -d)
//	go test ./internal/e2e
//	go tool covdata percent -i="$GOCOVERDIR"
package e2e
