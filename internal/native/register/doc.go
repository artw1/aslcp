// Package register keeps an outbound-only native node registered with the
// AllStarLink HTTPS registration service, the way ASL3's
// res_rpt_http_registrations module does. It also checks that the node's DNS
// record shows the public address the registrar saw, so callers can hold
// outgoing links until the record exists.
package register
