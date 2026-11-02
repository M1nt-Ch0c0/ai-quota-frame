package ble

import "context"

// UseSession runs later sends on a caller-owned authenticated session (the
// daemon's persistent link) instead of a new connection per image. The session
// must serve the same device and credentials this sender was created with.
func (s *AppImageSender) UseSession(withClient func(context.Context, func(context.Context, *Client) error) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.withClient = func(ctx context.Context, _, _ string, job func(context.Context, *Client) error) error {
		return withClient(ctx, job)
	}
}
