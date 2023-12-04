package firewall

import (
	"context"
	"net"
	"strings"
	"sync"

	grpcproxy "github.com/mwitkow/grpc-proxy/proxy"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type GrpcProxy struct {
	defaultAction RuleAction
	rules         []*GrpcRule
	listener      net.Listener
	server        *grpc.Server
	client        *grpc.ClientConn
	mu            sync.RWMutex
	log           *log.Entry
}

func NewGrpcProxy(name, localAddr, remoteAddr string, opts ...Option[GrpcProxyOptions]) (*GrpcProxy, error) {
	cfg := DefaultGrpcProxyOptions()
	for _, opt := range opts {
		opt(cfg)
	}

	lis, err := net.Listen("tcp", localAddr)
	if err != nil {
		return nil, err
	}

	grpcConn, err := grpc.Dial(
		remoteAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	proxy := GrpcProxy{
		log:      log.WithField("proxy", name),
		listener: lis,
		client:   grpcConn,
		server:   grpcproxy.NewProxy(grpcConn),
	}
	server := grpc.NewServer(grpc.UnknownServiceHandler(grpcproxy.TransparentHandler(proxy.Handle)))
	proxy.server = server

	return &proxy, nil
}

func (p *GrpcProxy) Start() error {
	p.log.WithField("address", p.listener.Addr().String()).Info("starting grpc proxy")
	return p.server.Serve(p.listener)
}

func (p *GrpcProxy) SetRules(rules []*GrpcRule, defaultAction RuleAction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = rules
	p.defaultAction = defaultAction
}

func (p *GrpcProxy) Handle(ctx context.Context, method string) (context.Context, *grpc.ClientConn, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	outCtx := metadata.NewOutgoingContext(ctx, md.Copy())

	// Always forward internal services
	if strings.HasPrefix(method, "/grpc.reflection") {
		return outCtx, p.client, nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, rule := range p.rules {
		match := rule.Match(method)
		if match {
			switch rule.Action {
			case RuleActionAllow:
				p.log.WithField("method", method).Info("request allowed")
				return ctx, p.client, nil

			case RuleActionDeny:
				p.log.WithField("method", method).Info("request denied")
				return ctx, nil, status.Errorf(codes.Unavailable, "Unauthorized")

			default:
				log.Errorf("unrecognized rule action %q", rule.Action)
			}
		}
	}

	if p.defaultAction == RuleActionAllow {
		return ctx, p.client, nil
	}
	return ctx, nil, status.Errorf(codes.Unavailable, "Unauthorized")
}
