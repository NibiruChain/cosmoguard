package cosmoguard

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

func TestJsonRpcRule_Match(t *testing.T) {
	table := []struct {
		Rule        JsonRpcRule
		ExpectMatch bool
		Request     *JsonRpcMsg
	}{
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{"status"},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "status",
			},
			ExpectMatch: true,
		},
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "status",
			},
			ExpectMatch: true,
		},
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{"status"},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "abci_query",
			},
			ExpectMatch: false,
		},
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{"abci_*"},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "abci_query",
				Params:  make(map[string]interface{}),
			},
			ExpectMatch: true,
		},
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{"abci_*"},
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/*",
				},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "abci_query",
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/AllBalances",
					"test": 1,
				},
			},
			ExpectMatch: true,
		},
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{"abci_*"},
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/*",
					"test": 1,
				},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "abci_query",
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/AllBalances",
					"test": 1,
				},
			},
			ExpectMatch: true,
		},
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{"abci_*"},
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/*",
					"test": 1,
				},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "abci_query",
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/AllBalances",
					"test": 2,
				},
			},
			ExpectMatch: false,
		},
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{},
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/*",
				},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "abci_query",
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/AllBalances",
				},
			},
			ExpectMatch: true,
		},
		{
			Rule: JsonRpcRule{
				Action:  RuleActionAllow,
				Methods: []string{"abci_*"},
				Params: map[string]interface{}{
					"path": "/cosmos.bank.v1beta1.Query/*",
				},
			},
			Request: &JsonRpcMsg{
				ID:      1234,
				Version: "2.0",
				Method:  "abci_query",
				Params: map[string]interface{}{
					"test": 1,
				},
			},
			ExpectMatch: false,
		},
	}

	for _, test := range table {
		test.Rule.Compile()
		assert.Equal(t, test.Rule.Match(test.Request), test.ExpectMatch)
	}
}
