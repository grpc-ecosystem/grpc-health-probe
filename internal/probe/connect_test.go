// Copyright 2018 Google LLC
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

package probe

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConnect_timeoutError(t *testing.T) {
	// attempt to connect a non-listening port would hang and timeout
	_, err := Connect(context.TODO(), "localhost:9999", nil, time.Millisecond*100)
	if err == nil {
		t.Fatal("was expecting failure")
	}
	errMsg := err.Error()
	expectedError := `timeout: failed to connect service "localhost:9999" within 100ms`
	if expectedError != errMsg {
		t.Fatalf("got=%q expected=%q", errMsg, expectedError)
	}
}

func TestConnect_genericError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// DialContext would fail on a canceled context
	_, err := Connect(ctx, "localhost:9999", nil, time.Millisecond*100)
	if err == nil {
		t.Fatal("was expecting failure")
	}
	errMsg := err.Error()
	expectedPrefix := `error: failed to connect service at "localhost:9999"`
	if !strings.HasPrefix(errMsg, expectedPrefix) {
		t.Fatalf("was expecting prefix %q in error=%q", expectedPrefix, errMsg)
	}
}

func TestConnect_withCredentials(t *testing.T) {
	addr, close := makeServer(t, readCreds(t, testdata("127.0.0.1.pem"), testdata("127.0.0.1-key.pem")))
	defer close()

	creds, err := BuildCredentials(false, testdata("ca.pem"), testdata("127.0.0.1.pem"), testdata("127.0.0.1-key.pem"), "")
	if err != nil {
		t.Fatalf("failed to build credentials: %+v", creds)
	}

	conn, err := Connect(context.TODO(), addr, creds, time.Millisecond*500)
	if err != nil {
		t.Fatalf("failed to connect: %+v", err)
	}
	defer conn.Close()
}

func TestConnect_withoutCredentials(t *testing.T) {
	addr, close := makeServer(t, readCreds(t, testdata("127.0.0.1.pem"), testdata("127.0.0.1-key.pem")))
	defer close()

	conn, err := Connect(context.TODO(), addr, nil, time.Millisecond*100)
	if err != nil {
		t.Fatalf("failed to connect: %+v", err)
	}
	defer conn.Close()
}
