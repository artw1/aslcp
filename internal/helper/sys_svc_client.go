package helper

import (
	"context"
	"strings"
)

// Client methods of the services switch and the port 80 move (sys_svc.go,
// sys_webport.go). The API server uses them through its SvcHost interface;
// DemoSystem implements the same set.

var svcErrors = []error{ErrPort80Held, ErrSvcNotInstalled, ErrPort80Busy, ErrPort80Unchecked, ErrAlreadyPort80, ErrNotOnPort80}

// mapSvcErr turns the text of a helper error back into its sentinel.
func mapSvcErr(err error) error {
	if err == nil {
		return nil
	}
	for _, e := range svcErrors {
		if strings.HasPrefix(err.Error(), e.Error()) {
			return e
		}
	}
	return err
}

// SvcStatus calls svc-status.
func (c *Client) SvcStatus(ctx context.Context) (SvcStatus, error) {
	var v SvcStatus
	return v, c.sys(ctx, OpSvcStatus, nil, &v, 1<<20)
}

// SvcSet calls svc-set.
func (c *Client) SvcSet(ctx context.Context, scope, action string, firewall bool) (SvcSetResult, error) {
	var v SvcSetResult
	err := c.sys(ctx, OpSvcSet, &SysRequest{SvcScope: scope, SvcAction: action, SvcFirewall: firewall}, &v, 1<<20)
	return v, mapSvcErr(err)
}

// WebPortStatus calls web-port-status.
func (c *Client) WebPortStatus(ctx context.Context) (WebPortStatus, error) {
	var v WebPortStatus
	return v, c.sys(ctx, OpWebPortStatus, nil, &v, 1<<20)
}

// WebPortSet calls web-port-set.
func (c *Client) WebPortSet(ctx context.Context, action string) (WebPortStatus, error) {
	var v WebPortStatus
	err := c.sys(ctx, OpWebPortSet, &SysRequest{WebPortAction: action}, &v, 1<<20)
	return v, mapSvcErr(err)
}
