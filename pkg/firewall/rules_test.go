package firewall

import (
	"net/http"
	"net/url"
	"testing"

	"gotest.tools/assert"
)

func TestHttpRule_Match(t *testing.T) {
	table := []struct {
		Rule        HttpRule
		ExpectMatch bool
		Request     *http.Request
	}{
		{
			Rule: HttpRule{
				Priority: 1000,
				Action:   RuleActionAllow,
				Paths:    []string{"/path/to/a", "/path/to/b"},
				Methods:  []string{"GET", "POST"},
			},
			Request: &http.Request{
				Method: "GET",
				URL: &url.URL{
					Path: "/path/to/a",
				},
			},
			ExpectMatch: true,
		},
		{
			Rule: HttpRule{
				Priority: 1000,
				Action:   RuleActionAllow,
				Paths:    []string{"/path/to/a", "path/to/b"},
				Methods:  []string{"POST"},
			},
			Request: &http.Request{
				Method: "GET",
				URL: &url.URL{
					Path: "/path/to/a",
				},
			},
			ExpectMatch: false,
		},
		{
			Rule: HttpRule{
				Priority: 1000,
				Action:   RuleActionAllow,
				Paths:    []string{"/path/to/*"},
				Methods:  []string{"GET"},
			},
			Request: &http.Request{
				Method: "GET",
				URL: &url.URL{
					Path: "/path/to/a",
				},
			},
			ExpectMatch: true,
		},
		{
			Rule: HttpRule{
				Priority: 1000,
				Action:   RuleActionAllow,
				Paths:    []string{"/path/to/*"},
			},
			Request: &http.Request{
				Method: "GET",
				URL: &url.URL{
					Path: "/path/to/a",
				},
			},
			ExpectMatch: true,
		},
		{
			Rule: HttpRule{
				Priority: 1000,
				Action:   RuleActionAllow,
				Paths:    []string{"/path/to/*/test"},
				Methods:  []string{"GET"},
			},
			Request: &http.Request{
				Method: "GET",
				URL: &url.URL{
					Path: "/path/to/a/test",
				},
			},
			ExpectMatch: true,
		},
	}

	for _, test := range table {
		test.Rule.Compile()
		assert.Equal(t, test.Rule.Match(test.Request), test.ExpectMatch)
	}
}
