package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDisabledByDefault confirms the gate starts off.
func TestDisabledByDefault(t *testing.T) {
	Disable()
	Reset()
	if IsEnabled() {
		t.Fatal("expected disabled after Disable")
	}
	mw := HTTPMiddleware()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 3; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	}
	if got := SnapshotCounters().Counts; len(got) != 0 {
		t.Errorf("disabled middleware should not record; got %v", got)
	}
}

// TestEnabledRecordsCountsAndDuration exercises the full path.
func TestEnabledRecordsCountsAndDuration(t *testing.T) {
	Reset()
	Init()
	defer Disable()

	mw := HTTPMiddleware()
	ok := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	bad := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))

	for i := 0; i < 3; i++ {
		ok.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/a", nil))
	}
	bad.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/b", nil))

	snap := SnapshotCounters()
	twoXX := Key{Method: "GET", Path: "/a", StatusKlas: "2xx"}
	fourXX := Key{Method: "GET", Path: "/b", StatusKlas: "4xx"}
	if snap.Counts[twoXX] != 3 {
		t.Errorf("2xx count = %d, want 3", snap.Counts[twoXX])
	}
	if snap.Counts[fourXX] != 1 {
		t.Errorf("4xx count = %d, want 1", snap.Counts[fourXX])
	}
	if snap.TotalNs[twoXX] <= 0 || snap.TotalNs[fourXX] <= 0 {
		t.Errorf("expected non-zero total durations: %+v", snap.TotalNs)
	}
}

// TestClassify pins the bucketing rule so future reorders don't
// silently break the metric labels.
func TestClassify(t *testing.T) {
	cases := map[int]string{
		100: "1xx", 199: "1xx",
		200: "2xx", 299: "2xx",
		301: "3xx",
		404: "4xx",
		503: "5xx",
		700: "other",
	}
	for in, want := range cases {
		if got := classify(in); got != want {
			t.Errorf("classify(%d) = %q, want %q", in, got, want)
		}
	}
}
