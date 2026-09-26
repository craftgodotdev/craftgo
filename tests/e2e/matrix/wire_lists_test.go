package matrix

import (
	"io"
	"net/http"
	"testing"
)

// A list header binds every element of every X-Ids line, split at commas.
func TestListHeaderSplitsAtCommas(t *testing.T) {
	ts := bootAll(t)
	for _, tc := range []struct {
		name  string
		lines []string
		want  int
	}{
		{"one line", []string{"1,2"}, http.StatusOK},
		{"spaces", []string{"1, 2, 3"}, http.StatusOK},
		{"two lines", []string{"1", "2"}, http.StatusOK},
		{"too few", []string{"1"}, http.StatusBadRequest},
		{"bad element", []string{"1,x"}, http.StatusBadRequest},
	} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/bindings/ids", nil)
		for _, l := range tc.lines {
			req.Header.Add("X-Ids", l)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: got %d %s, want %d", tc.name, resp.StatusCode, raw, tc.want)
		}
	}
}
