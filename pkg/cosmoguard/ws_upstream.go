package cosmoguard

import (
	"errors"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmoguard/pkg/util"
)

const (
	jsonRpcVersion     = "2.0"
	connectTimeout     = 10 * time.Second
	connectRetryPeriod = 5 * time.Second
	responseTimeout    = 10 * time.Second
)

var (
	ErrSubscriptionExists = errors.New("subscription already exists")
)

type UpstreamConnManagerConstructor func(url.URL, *util.UniqueID, func(msg *JsonRpcMsg)) UpstreamConnManager

type UpstreamConnManager interface {
	Run(*log.Entry) error
	MakeRequest(*JsonRpcMsg) (*JsonRpcMsg, error)
	HasSubscription(string) bool
	Subscribe(string) (string, error)
	Unsubscribe(string) error
}
