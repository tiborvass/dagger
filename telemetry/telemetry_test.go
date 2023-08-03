package telemetry

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/progrock"
)

func TestTelemetry(t *testing.T) {
	f, err := os.Open("testdata/journal.log")
	require.NoError(t, err)
	defer f.Close()

	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	count := 0
	s := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		d := json.NewDecoder(r.Body)
		var ev struct {
			Event
			Payload json.RawMessage `json:"payload"`
		}
		for {
			err := d.Decode(&ev)
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			count++
		}
		require.NoError(t, err)
	})}
	defer s.Shutdown(nil)
	go s.Serve(ln)

	tm := New()
	tm.token = "TEST-TOKEN"
	tm.pushURL = "http://" + ln.Addr().String()
	tm.Start()
	require.True(t, tm.Enabled())

	t.Logf("Telemetry test server: %s", tm.pushURL)
	w := NewWriter(tm)
	defer w.Close()

	d := json.NewDecoder(f)
	var ev progrock.StatusUpdate
	expectedCount := 0
	for {
		err = d.Decode(&ev)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		err = w.WriteStatus(&ev)
		require.NoError(t, err)
		expectedCount++
	}
	w.Close()
	require.Equal(t, count, expectedCount)
}
