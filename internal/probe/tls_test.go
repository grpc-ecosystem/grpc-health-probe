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
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func Test_makeServer_ServerNoTLS_ClientNoTLS(t *testing.T) {
	addr, close := makeServer(t)
	defer close()
	conn := connect(t, addr, grpc.WithInsecure())
	defer conn.Close()
	makeRequest(t, conn)
}

func TestBuildCredentials_withSkipVerifyNoClientKeyPair(t *testing.T) {
	creds, err := BuildCredentials(true, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	addr, close := makeServer(t, readCreds(t, testdata("127.0.0.1.pem"), testdata("127.0.0.1-key.pem")))
	defer close()

	conn := connect(t, addr, grpc.WithTransportCredentials(creds))
	defer conn.Close()
	makeRequest(t, conn)
}

func TestBuildCredentials_withClientKeyPair(t *testing.T) {
	creds, err := BuildCredentials(false, testdata("ca.pem"), testdata("127.0.0.1.pem"), testdata("127.0.0.1-key.pem"), "")
	if err != nil {
		t.Fatal(err)
	}

	addr, close := makeServer(t, readCreds(t, testdata("127.0.0.1.pem"), testdata("127.0.0.1-key.pem")))
	defer close()

	conn := connect(t, addr, grpc.WithTransportCredentials(creds))
	defer conn.Close()
	makeRequest(t, conn)
}

func TestBuildCredentials_withServerNameOverride(t *testing.T) {
	creds, err := BuildCredentials(false, testdata("ca.pem"), testdata("example.com.pem"), testdata("example.com-key.pem"), "example.com")
	if err != nil {
		t.Fatal(err)
	}

	addr, close := makeServer(t, readCreds(t, testdata("example.com.pem"), testdata("example.com-key.pem")))
	defer close()
	conn := connect(t, addr, grpc.WithTransportCredentials(creds))
	defer conn.Close()
	makeRequest(t, conn)
}

func readCreds(t *testing.T, certFile, keyFile string) grpc.ServerOption {
	c, err := credentials.NewServerTLSFromFile(certFile, keyFile)
	if err != nil {
		t.Fatalf("failed to read server tls creds from file: %+v", err)
	}
	return grpc.Creds(c)
}
