package client

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/tg"
)

type AddContactReq struct {
	UserID     int64
	FirstName  string
	LastName   string
	SharePhone bool
}

func ValidateContactName(first, last string) error {
	if strings.TrimSpace(first) == "" || !utf8.ValidString(first) || !utf8.ValidString(last) || utf8.RuneCountInString(first) > 64 || utf8.RuneCountInString(last) > 64 {
		return safety.NewBadArgs("first name is required; contact names must be valid UTF-8 and at most 64 characters each")
	}
	return nil
}

func (g *GotdClient) contactUser(ctx context.Context, id int64) (tg.InputUserClass, error) {
	if id <= 0 || id == g.selfID {
		return nil, safety.NewBadArgs("select another user, not self or a group/channel")
	}
	peer, err := g.peerFromChatID(ctx, id)
	if err != nil {
		return nil, err
	}
	u, ok := peer.(*tg.InputPeerUser)
	if !ok {
		return nil, safety.NewBadArgs("contact target must be a user")
	}
	return &tg.InputUser{UserID: u.UserID, AccessHash: u.AccessHash}, nil
}

func (g *GotdClient) AddContact(ctx context.Context, req AddContactReq) error {
	if err := ValidateContactName(req.FirstName, req.LastName); err != nil {
		return err
	}
	u, err := g.contactUser(ctx, req.UserID)
	if err != nil {
		return err
	}
	_, err = g.api.ContactsAddContact(ctx, &tg.ContactsAddContactRequest{ID: u, FirstName: req.FirstName, LastName: req.LastName, AddPhonePrivacyException: req.SharePhone})
	return mapRPCErr(err)
}

func (g *GotdClient) RemoveContact(ctx context.Context, id int64) error {
	u, err := g.contactUser(ctx, id)
	if err != nil {
		return err
	}
	_, err = g.api.ContactsDeleteContacts(ctx, []tg.InputUserClass{u})
	return mapRPCErr(err)
}
