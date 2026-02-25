package ingress

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSecretToTLSCertPair(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n"),
			corev1.TLSPrivateKeyKey: []byte("-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n"),
		},
	}

	pair, err := SecretToTLSCertPair(secret, []string{"example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pair.CertData) == 0 {
		t.Error("expected CertData to be populated")
	}
	if len(pair.KeyData) == 0 {
		t.Error("expected KeyData to be populated")
	}
	if len(pair.Hosts) != 1 || pair.Hosts[0] != "example.com" {
		t.Errorf("expected hosts [example.com], got %v", pair.Hosts)
	}
}

func TestSecretToTLSCertPairMissingKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey: []byte("cert"),
		},
	}
	_, err := SecretToTLSCertPair(secret, nil)
	if err == nil {
		t.Error("expected error for missing key data")
	}
}

func TestResolveTLSCertPairs(t *testing.T) {
	store := NewStore()
	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sec1", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("cert1"),
			corev1.TLSPrivateKeyKey: []byte("key1"),
		},
	})

	entries := []TLSEntry{
		{SecretName: "sec1", Hosts: []string{"a.example.com"}},
		{SecretName: "missing", Hosts: []string{"b.example.com"}},
	}

	pairs, warnings := ResolveTLSCertPairs(store, "default", entries)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
}
