package ingress

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/wudi/runway/config"
)

// SecretToTLSCertPair extracts TLS PEM data from a Kubernetes Secret
// and returns a TLSCertPair with in-memory CertData/KeyData.
func SecretToTLSCertPair(secret *corev1.Secret, hosts []string) (config.TLSCertPair, error) {
	certData, ok := secret.Data[corev1.TLSCertKey]
	if !ok || len(certData) == 0 {
		return config.TLSCertPair{}, fmt.Errorf("secret %s/%s missing %s", secret.Namespace, secret.Name, corev1.TLSCertKey)
	}
	keyData, ok := secret.Data[corev1.TLSPrivateKeyKey]
	if !ok || len(keyData) == 0 {
		return config.TLSCertPair{}, fmt.Errorf("secret %s/%s missing %s", secret.Namespace, secret.Name, corev1.TLSPrivateKeyKey)
	}
	return config.TLSCertPair{
		CertData: certData,
		KeyData:  keyData,
		Hosts:    hosts,
	}, nil
}

// ResolveTLSCertPairs looks up TLS Secrets referenced by Ingress TLS entries
// and returns a slice of TLSCertPairs for in-memory certificate loading.
func ResolveTLSCertPairs(store *Store, namespace string, tlsEntries []TLSEntry) ([]config.TLSCertPair, []string) {
	var pairs []config.TLSCertPair
	var warnings []string

	for _, entry := range tlsEntries {
		if entry.SecretName == "" {
			continue
		}
		key := types.NamespacedName{Namespace: namespace, Name: entry.SecretName}
		secret, ok := store.GetSecret(key)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("TLS secret %s not found", key))
			continue
		}
		pair, err := SecretToTLSCertPair(secret, entry.Hosts)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		pairs = append(pairs, pair)
	}

	return pairs, warnings
}

// TLSEntry is a simplified TLS reference used by both Ingress and Gateway translators.
type TLSEntry struct {
	SecretName string
	Hosts      []string
}
