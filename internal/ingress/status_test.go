package ingress

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsIP(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"::1", true},
		{"fe80::1", true},
		{"example.com", false},
		{"my-service.default.svc", false},
	}
	for _, tt := range tests {
		if got := isIP(tt.input); got != tt.want {
			t.Errorf("isIP(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSetCondition(t *testing.T) {
	conds := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotReady"},
	}
	setCondition(&conds, metav1.Condition{
		Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready",
	})
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Status != metav1.ConditionTrue {
		t.Errorf("expected status True, got %s", conds[0].Status)
	}

	// Add new condition
	setCondition(&conds, metav1.Condition{
		Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted",
	})
	if len(conds) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conds))
	}
}
