package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChain(t *testing.T) {
	var order []string

	m1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m1-before")
			next.ServeHTTP(w, r)
			order = append(order, "m1-after")
		})
	}

	m2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m2-before")
			next.ServeHTTP(w, r)
			order = append(order, "m2-after")
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusOK)
	})

	chain := NewChain(m1, m2)
	final := chain.Then(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	expected := []string{"m1-before", "m2-before", "handler", "m2-after", "m1-after"}

	if len(order) != len(expected) {
		t.Errorf("Expected %d calls, got %d", len(expected), len(order))
	}

	for i, v := range expected {
		if i < len(order) && order[i] != v {
			t.Errorf("At index %d: expected %s, got %s", i, v, order[i])
		}
	}
}

func TestChainAppend(t *testing.T) {
	var order []string

	m1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m1")
			next.ServeHTTP(w, r)
		})
	}

	m2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m2")
			next.ServeHTTP(w, r)
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})

	chain := NewChain(m1)
	chain = chain.Append(m2)

	final := chain.Then(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	expected := []string{"m1", "m2", "handler"}

	for i, v := range expected {
		if i < len(order) && order[i] != v {
			t.Errorf("At index %d: expected %s, got %s", i, v, order[i])
		}
	}
}

func TestChainPrepend(t *testing.T) {
	var order []string

	m1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m1")
			next.ServeHTTP(w, r)
		})
	}

	m2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m2")
			next.ServeHTTP(w, r)
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})

	chain := NewChain(m2)
	chain = chain.Prepend(m1)

	final := chain.Then(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	expected := []string{"m1", "m2", "handler"}

	for i, v := range expected {
		if i < len(order) && order[i] != v {
			t.Errorf("At index %d: expected %s, got %s", i, v, order[i])
		}
	}
}

func TestChainLen(t *testing.T) {
	m := func(next http.Handler) http.Handler { return next }

	chain := NewChain(m, m, m)

	if chain.Len() != 3 {
		t.Errorf("Expected length 3, got %d", chain.Len())
	}
}

func TestChainExtend(t *testing.T) {
	m := func(next http.Handler) http.Handler { return next }

	chain1 := NewChain(m, m)
	chain2 := NewChain(m)

	combined := chain1.Extend(chain2)

	if combined.Len() != 3 {
		t.Errorf("Expected length 3, got %d", combined.Len())
	}
}

func TestBuilder(t *testing.T) {
	var called bool

	m := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	builder := NewBuilder()
	builder.Use(m)

	final := builder.Handler(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if !called {
		t.Error("Middleware should have been called")
	}
}

func TestBuilderUseIf(t *testing.T) {
	var m1Called, m2Called bool

	m1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m1Called = true
			next.ServeHTTP(w, r)
		})
	}

	m2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m2Called = true
			next.ServeHTTP(w, r)
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	builder := NewBuilder()
	builder.UseIf(true, m1)
	builder.UseIf(false, m2)

	final := builder.Handler(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if !m1Called {
		t.Error("m1 should have been called")
	}

	if m2Called {
		t.Error("m2 should not have been called")
	}
}

func TestWrapFunc(t *testing.T) {
	var called bool

	fn := func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		called = true
		next.ServeHTTP(w, r)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	m := WrapFunc(fn)
	final := m(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if !called {
		t.Error("Wrapped function should have been called")
	}
}
