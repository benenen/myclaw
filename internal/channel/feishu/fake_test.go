package feishu

import "context"

type fakeAPI struct {
	validateInfo    AppInfo
	validateErr     error
	validatedAppID  string
	validatedSecret string
	sent            []sentMessage
	sendErr         error
}

type sentMessage struct {
	creds  Credentials
	params SendParams
}

func (f *fakeAPI) ValidateApp(_ context.Context, appID, appSecret string) (AppInfo, error) {
	f.validatedAppID = appID
	f.validatedSecret = appSecret
	if f.validateErr != nil {
		return AppInfo{}, f.validateErr
	}
	return f.validateInfo, nil
}

func (f *fakeAPI) SendText(_ context.Context, creds Credentials, p SendParams) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, sentMessage{creds: creds, params: p})
	return nil
}

type fakeDialer struct {
	conn      *fakeConn
	dialErr   error
	lastCreds Credentials
}

func (d *fakeDialer) Dial(creds Credentials, onMessage func(InboundMessage)) (conn, error) {
	d.lastCreds = creds
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	d.conn.onMessage = onMessage
	return d.conn, nil
}

type fakeConn struct {
	onMessage func(InboundMessage)
	started   chan struct{}
}

func newFakeConn() *fakeConn { return &fakeConn{started: make(chan struct{})} }

func (c *fakeConn) Start(ctx context.Context) error {
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

// inject simulates an inbound message arriving over the wire.
func (c *fakeConn) inject(msg InboundMessage) { c.onMessage(msg) }
