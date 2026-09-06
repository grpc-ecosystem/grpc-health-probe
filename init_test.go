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

package main

import (
	"os"
	"testing"
)

// main.go parses os.Args in its init() and exits on any unknown flag, which
// includes the -test.* flags every test binary is started with. Go initialises
// all package-level variables before running any init() function, so this
// variable's initializer runs first: it hides the real arguments and supplies
// the one required flag. TestMain restores the real arguments before the
// testing package parses them.
var testArgs = hideTestFlags()

func hideTestFlags() []string {
	saved := os.Args
	os.Args = []string{os.Args[0], "-addr", "unit-test:0"}
	return saved
}

func TestMain(m *testing.M) {
	os.Args = testArgs
	os.Exit(m.Run())
}
