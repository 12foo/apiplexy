package logging

import (
	"github.com/12foo/apiplexy"
	"math/rand"
	"net/http"
	"strings"
	"testing"
)

func assertNil(t *testing.T, what interface{}) {
	if what != nil {
		t.Errorf("Expected nil, got %v (%T).", what, what)
	}
}

func assertLength(t *testing.T, vals []string, length int) {
	if len(vals) != length {
		if len(vals) == 0 {
			t.Errorf("Expected %d entries, got none.", length)
		} else {
			t.Errorf("Expected %d entries, got the following:\n- %s\n", length, strings.Join(written, "\n- "))
		}
	}
}

func assertEqual(t *testing.T, val interface{}, correct interface{}) {
	if val != correct {
		t.Errorf("Expected %v (%T), got %v (%T).", correct, correct, val, val)
	}
}

func generateLog() (*http.Request, *http.Response, *apiplexy.APIContext) {
	req, _ := http.NewRequest("GET", "/test", nil)
	res := http.Response{}
	ctx := apiplexy.APIContext{
		Log: map[string]interface{}{
			"test_int":   rand.Int(),
			"test_float": rand.Float64(),
		},
	}
	return req, &res, &ctx
}

func runLogs(t *testing.T, logfunc func(*http.Request, *http.Response, *apiplexy.APIContext) error, count int) {
	for i := 0; i < count; i++ {
		assertNil(t, logfunc(generateLog()))
	}
}
