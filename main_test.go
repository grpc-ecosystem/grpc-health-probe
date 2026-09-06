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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
)

func TestRPCHeadersSet(t *testing.T) {
	for _, tc := range []struct {
		in      string
		key     string
		value   string
		wantErr bool
	}{
		{in: "key: value", key: "key", value: "value"},
		{in: "key:value", key: "key", value: "value"},
		{in: "key:    spaced", key: "key", value: "spaced"},
		{in: "key: a: b", key: "key", value: "a: b"},
		{in: "key: trailing ", key: "key", value: "trailing "},
		{in: "key:", key: "key", value: ""},
		{in: "novalue", wantErr: true},
		{in: "", wantErr: true},
	} {
		h := rpcHeaders{MD: metadata.MD{}}
		err := h.Set(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Set(%q): want error, got none", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("Set(%q): %v", tc.in, err)
			continue
		}
		if got := h.Get(tc.key); !reflect.DeepEqual(got, []string{tc.value}) {
			t.Errorf("Set(%q): header %q = %q, want [%q]", tc.in, tc.key, got, tc.value)
		}
	}
}

func TestRPCHeadersAppend(t *testing.T) {
	h := rpcHeaders{MD: metadata.MD{}}
	for _, v := range []string{"foo: 1", "foo: 2", "Bar: x"} {
		if err := h.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	if got := h.Get("foo"); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Errorf("foo = %q, want [1 2]", got)
	}
	// metadata.MD lowercases keys.
	if got := h.Get("bar"); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("bar = %q, want [x]", got)
	}
	if h.Len() != 2 {
		t.Errorf("Len() = %d, want 2", h.Len())
	}
	if s := h.String(); !strings.Contains(s, "foo") || !strings.Contains(s, "bar") {
		t.Errorf("String() = %q, want both keys mentioned", s)
	}
}

func TestBuildCredentials(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeSelfSignedPEM(t, dir, "server")
	_, otherKeyFile := writeSelfSignedPEM(t, dir, "other")
	garbageFile := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbageFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.pem")

	t.Run("insecure skip verify", func(t *testing.T) {
		creds, err := buildCredentials(true, "", "", "", "")
		if err != nil || creds == nil {
			t.Fatalf("got (%v, %v), want credentials", creds, err)
		}
	})
	t.Run("server name is applied", func(t *testing.T) {
		creds, err := buildCredentials(false, "", "", "", "example.com")
		if err != nil {
			t.Fatal(err)
		}
		if got := creds.Info().ServerName; got != "example.com" {
			t.Errorf("ServerName = %q, want example.com", got)
		}
	})
	t.Run("ca cert file", func(t *testing.T) {
		if _, err := buildCredentials(false, certFile, "", "", ""); err != nil {
			t.Fatalf("valid CA file: %v", err)
		}
	})
	t.Run("ca cert file missing", func(t *testing.T) {
		_, err := buildCredentials(false, missing, "", "", "")
		if err == nil || !strings.Contains(err.Error(), "failed to load root CA certificates") {
			t.Fatalf("got %v, want load error", err)
		}
	})
	t.Run("ca cert file without certificates", func(t *testing.T) {
		_, err := buildCredentials(false, garbageFile, "", "", "")
		if err == nil || !strings.Contains(err.Error(), "no root CA certs parsed") {
			t.Fatalf("got %v, want parse error", err)
		}
	})
	t.Run("client cert and key", func(t *testing.T) {
		if _, err := buildCredentials(false, "", certFile, keyFile, ""); err != nil {
			t.Fatalf("valid pair: %v", err)
		}
	})
	t.Run("client cert with mismatched key", func(t *testing.T) {
		_, err := buildCredentials(false, "", certFile, otherKeyFile, "")
		if err == nil || !strings.Contains(err.Error(), "failed to load tls client cert/key pair") {
			t.Fatalf("got %v, want key pair error", err)
		}
	})
	t.Run("client cert file missing", func(t *testing.T) {
		if _, err := buildCredentials(false, "", missing, keyFile, ""); err == nil {
			t.Fatal("want error for missing client cert")
		}
	})
}

func TestProbeVersion(t *testing.T) {
	if v := probeVersion(); v == "" {
		t.Error("probeVersion() is empty")
	}
	saved := versionTag
	defer func() { versionTag = saved }()
	versionTag = "v1.2.3"
	if v := probeVersion(); !strings.HasPrefix(v, "v1.2.3; ") {
		t.Errorf("probeVersion() with tag = %q, want prefix \"v1.2.3; \"", v)
	}
}

// writeSelfSignedPEM writes a self-signed certificate and its key into dir and
// returns their paths.
func writeSelfSignedPEM(t *testing.T, dir, name string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
