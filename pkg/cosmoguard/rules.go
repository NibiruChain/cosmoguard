package cosmoguard

import (
	"fmt"
	"net/http"

	"github.com/gobwas/glob"

	"github.com/NibiruChain/cosmoguard/pkg/util"
)

type RuleAction string

const (
	RuleActionAllow = "allow"
	RuleActionDeny  = "deny"
)

type HttpRule struct {
	Priority  int         `yaml:"priority,omitempty" default:"1000"`
	Action    RuleAction  `yaml:"action"`
	Paths     []string    `yaml:"paths,omitempty"`
	Methods   []string    `yaml:"methods,omitempty"`
	Cache     *RuleCache  `yaml:"cache,omitempty"`
	PathGlobs []glob.Glob `yaml:"-"`
}

func (r *HttpRule) String() string {
	return fmt.Sprintf("%d - %s - %v - %v", r.Priority, r.Action, r.Methods, r.Paths)
}

func (r *HttpRule) Compile() {
	if len(r.Paths) > 0 {
		r.PathGlobs = make([]glob.Glob, len(r.Paths))
		for i, p := range r.Paths {
			r.PathGlobs[i] = glob.MustCompile(p, '/')
		}
	}
}

func (r *HttpRule) Match(req *http.Request) bool {
	if len(r.Methods) > 0 && !util.SliceContainsStringIgnoreCase(r.Methods, req.Method) {
		return false
	}
	if len(r.Paths) == 0 {
		return true
	}
	for _, g := range r.PathGlobs {
		if g.Match(req.URL.Path) {
			return true
		}
	}
	return false
}

type JsonRpcRule struct {
	Priority    int                    `yaml:"priority,omitempty" default:"1000"`
	Action      RuleAction             `yaml:"action"`
	Methods     []string               `yaml:"methods,omitempty"`
	Params      map[string]interface{} `yaml:"params,omitempty"`
	Cache       *RuleCache             `yaml:"cache,omitempty"`
	MethodGlobs []glob.Glob            `yaml:"-"`
	ParamsGlobs map[string]glob.Glob   `yaml:"-"`
}

func (r *JsonRpcRule) String() string {
	return fmt.Sprintf("%d - %s - %v - %v", r.Priority, r.Action, r.Methods, r.Params)
}

func (r *JsonRpcRule) Compile() {
	if len(r.Methods) > 0 {
		r.MethodGlobs = make([]glob.Glob, len(r.Methods))
		for i, p := range r.Methods {
			r.MethodGlobs[i] = glob.MustCompile(p)
		}
	}
	if r.ParamsGlobs == nil {
		r.ParamsGlobs = make(map[string]glob.Glob)
	}
	for key, v := range r.Params {
		if value, ok := v.(string); ok {
			r.ParamsGlobs[key] = glob.MustCompile(value, '/')
		}
	}
}

func (r *JsonRpcRule) Match(req *JsonRpcMsg) bool {
	if len(r.Params) == 0 {
		if len(r.Methods) == 0 {
			return true
		}
		for _, g := range r.MethodGlobs {
			if g.Match(req.Method) {
				return true
			}
		}
		return false
	}
	requestParams, ok := req.Params.(map[string]interface{})
	if !ok {
		// These rules only accept maps as params
		return false
	}

	for key, v := range r.Params {
		if g, ok := r.ParamsGlobs[key]; ok {
			reqV, exists := requestParams[key]
			if !exists {
				return false
			}
			str, ok := reqV.(string)
			if !ok {
				return false
			}
			if !g.Match(str) {
				return false
			}
		} else {
			if v != requestParams[key] {
				return false
			}
		}
	}
	return true
}

type GrpcRule struct {
	Priority    int         `yaml:"priority,omitempty" default:"1000"`
	Action      RuleAction  `yaml:"action"`
	Methods     []string    `yaml:"methods,omitempty"`
	MethodGlobs []glob.Glob `yaml:"-"`
}

func (r *GrpcRule) String() string {
	return fmt.Sprintf("%d - %s - %v", r.Priority, r.Action, r.Methods)
}

func (r *GrpcRule) Compile() {
	if len(r.Methods) > 0 {
		r.MethodGlobs = make([]glob.Glob, len(r.Methods))
		for i, p := range r.Methods {
			r.MethodGlobs[i] = glob.MustCompile(p, '/')
		}
	}
}

func (r *GrpcRule) Match(method string) bool {
	if len(r.Methods) == 0 {
		return true
	}
	for _, g := range r.MethodGlobs {
		if g.Match(method) {
			return true
		}
	}
	return false
}
