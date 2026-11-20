package helper

import "context"

// Client methods of the System tab ops (sys.go). The API server uses them
// through its SystemHost interface; DemoSystem implements the same set.

// maxLogsResponse bounds logs-read responses (the text is JSON-escaped).
const maxLogsResponse = 2 << 20

func (c *Client) sys(ctx context.Context, op string, sr *SysRequest, out any, max int64) error {
	return c.do(ctx, Request{Op: op, Sys: sr}, out, max)
}

// HostStatus calls host-status.
func (c *Client) HostStatus(ctx context.Context) (HostStatus, error) {
	var v HostStatus
	return v, c.sys(ctx, OpHostStatus, nil, &v, 1<<20)
}

// SetHostname calls host-set-hostname.
func (c *Client) SetHostname(ctx context.Context, name string) (HostStatus, error) {
	var v HostStatus
	return v, c.sys(ctx, OpHostSetHostname, &SysRequest{Hostname: name}, &v, 1<<20)
}

// SetTimezone calls host-set-timezone.
func (c *Client) SetTimezone(ctx context.Context, tz string) (HostStatus, error) {
	var v HostStatus
	return v, c.sys(ctx, OpHostSetTimezone, &SysRequest{Timezone: tz}, &v, 1<<20)
}

// SetNTP calls host-set-ntp.
func (c *Client) SetNTP(ctx context.Context, on bool) (HostStatus, error) {
	var v HostStatus
	return v, c.sys(ctx, OpHostSetNTP, &SysRequest{NTP: &on}, &v, 1<<20)
}

// Timezones calls host-timezones.
func (c *Client) Timezones(ctx context.Context) ([]string, error) {
	var v TimezoneList
	err := c.sys(ctx, OpHostTimezones, nil, &v, 1<<20)
	return v.Timezones, err
}

// Poweroff calls host-poweroff.
func (c *Client) Poweroff(ctx context.Context) error {
	var v OKResult
	return c.sys(ctx, OpHostPoweroff, nil, &v, 1<<20)
}

// Logs calls logs-read.
func (c *Client) Logs(ctx context.Context, unit string, lines int, since string) (LogsResult, error) {
	var v LogsResult
	return v, c.sys(ctx, OpLogsRead, &SysRequest{Unit: unit, Lines: lines, Since: since}, &v, maxLogsResponse)
}

// Services calls services-status.
func (c *Client) Services(ctx context.Context) (ServicesResult, error) {
	var v ServicesResult
	return v, c.sys(ctx, OpServicesStatus, nil, &v, 1<<20)
}

// Network calls net-status.
func (c *Client) Network(ctx context.Context) (NetStatus, error) {
	var v NetStatus
	return v, c.sys(ctx, OpNetStatus, nil, &v, 1<<20)
}
