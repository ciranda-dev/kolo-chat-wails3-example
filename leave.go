package main

//lint:file-ignore ST1005 these errors are sentences the window shows as they are

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log"
)

// ErrLeaveNotConfirmed is returned by a Leave without the token of the latest PlanLeave.
var ErrLeaveNotConfirmed = errors.New("Confirm first: open Leave conversation again and read what it deletes.")

// LeavePlan is what Leave will do, for the window to show before the user confirms. Delete is the conversation's
// folder, and Keep the app's own files and the names of the Others conversations, which stay. Founder says we settle
// changes to members and rooms, and Admins are the other admins who can take that over. Configured says its key came
// from -conversation-key or KOLO_CHAT_CONVERSATION_KEY, so a start with it enters the conversation again.
type LeavePlan struct {
	Token      string   `json:"token"`
	Name       string   `json:"name"`
	Dir        string   `json:"dir"`
	Delete     []string `json:"delete"`
	Keep       []string `json:"keep"`
	Others     int      `json:"others"`
	Founder    bool     `json:"founder"`
	Admins     []Member `json:"admins"`
	Configured bool     `json:"configured"`
}

// PlanLeave returns the token that leave needs, whether we settle the lists, and who could take that over. Each plan
// replaces the last one. Conversations fills in the paths.
func (c *ChatService) PlanLeave() (LeavePlan, error) {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if !c.isActive() {
		return LeavePlan{}, ErrNotInConversation
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return LeavePlan{}, err
	}
	plan := LeavePlan{Token: hex.EncodeToString(b), Dir: c.set.DataDir, Admins: []Member{}}
	for _, m := range c.Members() {
		switch {
		case m.Key == hex.EncodeToString(c.pub):
			plan.Founder = m.Seat
		case m.Admin && !m.Pending:
			plan.Admins = append(plan.Admins, m)
		}
	}
	c.mu.Lock()
	c.leaveToken = plan.Token
	c.mu.Unlock()
	return plan, nil
}

// handOverFor checks the token of the latest plan, without using it up, and hands settlement over to the admin to.
// It waits for that admin, until cancelLeave or the chat stops.
func (c *ChatService) handOverFor(token, to string) error {
	if err := c.checkLeave(token, false); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.mu.Lock()
	c.leaveStop = cancel
	c.mu.Unlock()
	defer cancel()
	return c.handOver(ctx, to)
}

func (c *ChatService) cancelLeave() {
	c.mu.Lock()
	stop := c.leaveStop
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// checkLeave says whether token is the one of the latest plan; use spends it.
func (c *ChatService) checkLeave(token string, use bool) error {
	c.mu.Lock()
	want, active := c.leaveToken, c.active
	if use {
		c.leaveToken = ""
	}
	c.mu.Unlock()
	if !active {
		return ErrNotInConversation
	}
	if want == "" || subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		return ErrLeaveNotConfirmed
	}
	return nil
}

// leave checks the token of the latest plan, which any leave uses up, leaves the conversation's topic and stops the
// chat. Conversations then deletes the folder. The others are not told: they keep us as a member and keep what we
// sent.
func (c *ChatService) leave(token string) error {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if err := c.checkLeave(token, true); err != nil {
		return err
	}
	c.sw.Leave(c.topic())
	if err := c.shutdown(); err != nil {
		log.Print("leave: ", err) // what could close is closed, and the folder goes anyway
	}
	return nil
}
