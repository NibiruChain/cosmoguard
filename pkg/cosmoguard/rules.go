package cosmoguard

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gobwas/glob"
	log "github.com/sirupsen/logrus"

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
	Priority int         `yaml:"priority,omitempty" default:"1000"`
	Action   RuleAction  `yaml:"action"`
	Methods  []string    `yaml:"methods,omitempty"`
	Params   interface{} `yaml:"params,omitempty"`
	Cache    *RuleCache  `yaml:"cache,omitempty"`

	MethodGlobs []glob.Glob          `yaml:"-"`
	ParamsGlobs map[string]glob.Glob `yaml:"-"`
	ParamsMap   bool                 `yaml:"-"`
	ParamsSlice bool                 `yaml:"-"`
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
	if r.Params != nil {
		if paramsMap, ok := r.Params.(map[string]interface{}); ok {
			r.ParamsMap = len(paramsMap) > 0
			for key, v := range paramsMap {
				if value, ok := v.(string); ok {
					r.ParamsGlobs[key] = glob.MustCompile(value, '/')
				}
			}
		}
		if paramsSlice, ok := r.Params.([]interface{}); ok {
			r.ParamsSlice = len(paramsSlice) > 0
			for i, v := range paramsSlice {
				if value, ok := v.(string); ok {
					r.ParamsGlobs[strconv.Itoa(i)] = glob.MustCompile(value, '/')
				}
			}
		}
	}
}

func (r *JsonRpcRule) Match(req *JsonRpcMsg) bool {
	// Check if method matches (if no methods configured on rule, we accept any)
	methodMatch := len(r.Methods) == 0
	for _, g := range r.MethodGlobs {
		if g.Match(req.Method) {
			methodMatch = true
		}
	}
	if !methodMatch {
		return false
	}

	// If we dont have any params configured on rule, its a match
	if !(r.ParamsMap || r.ParamsSlice) {
		return true
	}

	switch requestParams := req.Params.(type) {
	case map[string]interface{}:
		if !r.ParamsMap {
			return true
		}
		paramsMap, ok := r.Params.(map[string]interface{})
		if !ok {
			log.Errorf("jsonrpc: request params not a map: %v", requestParams)
			return false
		}
		for key, v := range paramsMap {
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
	case []interface{}:
		if !r.ParamsSlice {
			return true
		}
		paramsSlice, ok := r.Params.([]interface{})
		if !ok {
			log.Errorf("jsonrpc: request params not a slice: %v", requestParams)
			return false
		}
		for i, v := range paramsSlice {
			if g, ok := r.ParamsGlobs[strconv.Itoa(i)]; ok {
				str, ok := requestParams[i].(string)
				if !ok {
					return false
				}
				if !g.Match(str) {
					return false
				}
			} else {
				if v != requestParams[i] {
					return false
				}
			}
		}
		return true
	default:
		log.Warnf("unsupported params type: %T\n", requestParams)
		return false
	}
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
