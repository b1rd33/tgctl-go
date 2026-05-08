package client

import (
	"context"
	"errors"
)

// FakeClient is the test double for the Client interface. Command tests build
// one, set a Me value (or NextErr), and pass it to runners that depend on
// Client. Mirrors the test-only `fake_client` used in Python tests.
type FakeClient struct {
	Me      User
	NextErr error
	Closed  bool
	Calls   []string
}

func (f *FakeClient) GetMe(_ context.Context) (User, error) {
	f.Calls = append(f.Calls, "GetMe")
	if f.NextErr != nil {
		return User{}, f.NextErr
	}
	if f.Me.ID == 0 {
		return User{}, errors.New("fake client: Me not set")
	}
	return f.Me, nil
}

func (f *FakeClient) Close() error {
	f.Closed = true
	return nil
}
