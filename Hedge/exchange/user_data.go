package exchange

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

//

//

const (
	userDataKeepaliveInterval = 30 * time.Minute
	userDataRenewTimeout      = 10 * time.Second
)

type UserData struct {
	client *futures.Client
	proxy  string

	mu        sync.Mutex
	listenKey string
	ready     bool
	closed    bool
	stop      chan struct{}

	doneC chan struct{}
	stopC chan struct{}
}

func NewUserData(client *futures.Client, proxy string) *UserData {
	u := &UserData{
		client: client,
		proxy:  proxy,
		stop:   make(chan struct{}),
	}
	if u.proxy != "" {
		futures.SetWsProxyUrl(u.proxy)
	}
	go u.run()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = u.WaitReady(ctx)
	return u
}

func (u *UserData) WaitReady(ctx context.Context) error {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		u.mu.Lock()
		ready := u.ready && !u.closed
		u.mu.Unlock()
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-u.stop:
			return errors.New("UserData 已关闭")
		case <-t.C:
		}
	}
}

func (u *UserData) Close() {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return
	}
	u.closed = true
	close(u.stop)
	stopC := u.stopC
	u.stopC = nil
	u.mu.Unlock()
	if stopC != nil {
		close(stopC)
	}
	u.closeListenKey()
}

func (u *UserData) run() {
	backoff := time.Second
	for {
		u.mu.Lock()
		closed := u.closed
		u.mu.Unlock()
		if closed {
			return
		}

		if u.listenKey == "" {
			lk, err := u.newListenKey()
			if err != nil {
				if !u.sleep(backoff) {
					return
				}
				backoff = nextBackoff(backoff)
				continue
			}
			u.mu.Lock()
			u.listenKey = lk
			u.mu.Unlock()
			backoff = time.Second
		}

		doneC, stopC, err := futures.WsUserDataServe(u.listenKey, u.wsHandler, u.wsErrHandler)
		if err != nil {
			u.clearListenKey()
			if !u.sleep(backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		u.mu.Lock()
		if u.closed {
			u.mu.Unlock()
			close(stopC)
			return
		}
		u.doneC = doneC
		u.stopC = stopC
		u.ready = true
		u.mu.Unlock()
		backoff = time.Second

		keepaliveStop := make(chan struct{})
		go u.keepalive(keepaliveStop)

		select {
		case <-doneC:
		case <-u.stop:
		}
		close(keepaliveStop)

		u.clearConn()

		u.mu.Lock()
		closed = u.closed
		u.mu.Unlock()
		if closed {
			return
		}

		u.clearListenKey()
	}
}

func (u *UserData) wsHandler(event *futures.WsUserDataEvent) {

	if event.Event == futures.UserDataEventTypeListenKeyExpired {
		u.clearListenKey()
		u.closeStopC()
		return
	}
	go u.userHandler(event)
}

func (u *UserData) wsErrHandler(err error) {
	_ = err
}

func (u *UserData) newListenKey() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), userDataRenewTimeout)
	defer cancel()
	return u.client.NewStartUserStreamService().Do(ctx)
}

func (u *UserData) closeListenKey() {
	u.mu.Lock()
	lk := u.listenKey
	u.mu.Unlock()
	if lk == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), userDataRenewTimeout)
	defer cancel()
	if err := u.client.NewCloseUserStreamService().ListenKey(lk).Do(ctx); err != nil {
		return
	}
}

func (u *UserData) keepalive(stop chan struct{}) {
	renew := time.NewTicker(userDataKeepaliveInterval)
	defer renew.Stop()
	for {
		select {
		case <-renew.C:
			u.renewListenKey()
		case <-stop:
			return
		}
	}
}

func (u *UserData) renewListenKey() {
	u.mu.Lock()
	lk := u.listenKey
	u.mu.Unlock()
	if lk == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), userDataRenewTimeout)
	defer cancel()
	if err := u.client.NewKeepaliveUserStreamService().ListenKey(lk).Do(ctx); err != nil {
		u.clearListenKey()
		u.closeStopC()
		return
	}
}

func (u *UserData) clearListenKey() {
	u.mu.Lock()
	u.listenKey = ""
	u.mu.Unlock()
}

func (u *UserData) clearConn() {
	u.mu.Lock()
	u.doneC = nil
	u.ready = false
	stopC := u.stopC
	u.stopC = nil
	u.mu.Unlock()
	if stopC != nil {
		close(stopC)
	}
}

func (u *UserData) closeStopC() {
	u.mu.Lock()
	stopC := u.stopC
	u.stopC = nil
	u.mu.Unlock()
	if stopC != nil {
		close(stopC)
	}
}

func (u *UserData) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-u.stop:
		return false
	}
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}
