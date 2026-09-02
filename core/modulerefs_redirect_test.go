package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDaggerGetEligible(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{"www.marcosnils.com/go", true},
		{"https://www.marcosnils.com/go", true},
		{"github.com/dagger/dagger", true},
		{"www.marcosnils.com/go@v1.2.3", true},
		{"http://example.com/mod", false},
		{"ssh://git@github.com/foo/bar", false},
		{"git://example.com/foo", false},
		{"git@github.com:foo/bar", false},
		{"./local/path", false},
		{"/abs/path", false},
		{"nodot/path", false},
		{"", false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			require.Equal(t, tc.want, daggerGetEligible(tc.ref))
		})
	}
}

func TestDaggerGetProbe(t *testing.T) {
	// Server redirects only the exact dagger-get probe and echoes the flag back
	// in Location to verify it gets stripped.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get(daggerGetQueryParam) == "1" && r.URL.Path == "/go" {
			http.Redirect(w, r, "https://github.com/dagger/dagger?dagger-get=1", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	host := srv.Listener.Addr().String()

	t.Run("redirect strips dagger-get and preserves version", func(t *testing.T) {
		got := daggerGetProbe(t.Context(), "https://"+host+"/go@v1.2.3")
		require.Equal(t, "https://github.com/dagger/dagger@v1.2.3", got)
	})

	t.Run("redirect without version", func(t *testing.T) {
		got := daggerGetProbe(t.Context(), "https://"+host+"/go")
		require.Equal(t, "https://github.com/dagger/dagger", got)
	})

	t.Run("no redirect falls back to original", func(t *testing.T) {
		ref := "https://" + host + "/other"
		require.Equal(t, ref, daggerGetProbe(t.Context(), ref))
	})
}

func TestDaggerGetProbeRejectsNonHTTPSLocation(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://github.com/dagger/dagger", http.StatusFound)
	}))
	defer srv.Close()

	oldClient := daggerGetClient
	daggerGetClient = srv.Client()
	daggerGetClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { daggerGetClient = oldClient }()

	ref := "https://" + srv.Listener.Addr().String() + "/go"
	require.Equal(t, ref, daggerGetProbe(t.Context(), ref))
}
