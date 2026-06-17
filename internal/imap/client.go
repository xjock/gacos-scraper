package imap

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/textproto"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/xjock/gacos-scraper/internal/utils"
)

// Client polls an IMAP mailbox for GACOS download links.
type Client struct {
	Server        string
	Username      string
	Password      string
	UseTLS        bool
	SkipTLSVerify bool
	Mailbox       string
	SenderFilter  string
	SubjectFilter string
}

// Email holds a parsed message with its UID and raw body.
type Email struct {
	UID    uint32
	From   string
	Subject string
	Body   []byte
}

// Poll searches the mailbox for unread emails since `since` and extracts tar.gz URLs.
// It returns the URLs together with the message UID for marking as seen later.
func (c *Client) Poll(ctx context.Context, since time.Time) ([]string, map[string]uint32, error) {
	if c.Mailbox == "" {
		c.Mailbox = "INBOX"
	}

	var cl *client.Client
	var err error
	if c.UseTLS {
		tlsConfig := &tls.Config{InsecureSkipVerify: c.SkipTLSVerify}
		cl, err = client.DialTLS(c.Server, tlsConfig)
	} else {
		cl, err = client.Dial(c.Server)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("connect to imap %s: %w", c.Server, err)
	}
	defer cl.Logout()

	if err := cl.Login(c.Username, c.Password); err != nil {
		return nil, nil, fmt.Errorf("imap login: %w", err)
	}

	// 163.com requires the IMAP ID command to be sent before mailbox access.
	if err := sendID(cl); err != nil {
		return nil, nil, fmt.Errorf("imap id: %w", err)
	}

	mbox, err := cl.Select(c.Mailbox, false)
	if err != nil {
		return nil, nil, fmt.Errorf("select mailbox %s: %w", c.Mailbox, err)
	}
	if mbox.Messages == 0 {
		return nil, nil, nil
	}

	criteria := imap.NewSearchCriteria()
	// Search all emails since the given date, not just unseen ones.
	// The orchestrator uses URL deduplication to avoid re-downloading
	// archives that have already been processed.
	if !since.IsZero() {
		criteria.Since = since
	}
	if c.SenderFilter != "" {
		criteria.Header = textproto.MIMEHeader{}
		criteria.Header.Set("From", c.SenderFilter)
	}
	if c.SubjectFilter != "" {
		if criteria.Header == nil {
			criteria.Header = textproto.MIMEHeader{}
		}
		criteria.Header.Set("Subject", c.SubjectFilter)
	}

	uids, err := cl.UidSearch(criteria)
	if err != nil {
		return nil, nil, fmt.Errorf("imap search: %w", err)
	}
	if len(uids) == 0 {
		return nil, nil, nil
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uids...)

	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{section.FetchItem(), imap.FetchUid, imap.FetchEnvelope}

	messages := make(chan *imap.Message, len(uids))
	done := make(chan error, 1)
	go func() {
		done <- cl.UidFetch(seqSet, items, messages)
	}()

	var urls []string
	urlToUID := make(map[string]uint32)
	for msg := range messages {
		uid := msg.Uid
		r := msg.GetBody(section)
		if r == nil {
			continue
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(r); err != nil {
			continue
		}
		body := buf.Bytes()
		found := utils.ExtractTargzURLs(string(body))
		for _, u := range found {
			if _, ok := urlToUID[u]; !ok {
				urls = append(urls, u)
				urlToUID[u] = uid
			}
		}
	}
	if err := <-done; err != nil {
		return nil, nil, fmt.Errorf("imap fetch: %w", err)
	}

	return urls, urlToUID, nil
}

// MarkSeen marks a set of messages as read.
func (c *Client) MarkSeen(ctx context.Context, uids ...uint32) error {
	if len(uids) == 0 {
		return nil
	}
	var cl *client.Client
	var err error
	if c.UseTLS {
		tlsConfig := &tls.Config{InsecureSkipVerify: c.SkipTLSVerify}
		cl, err = client.DialTLS(c.Server, tlsConfig)
	} else {
		cl, err = client.Dial(c.Server)
	}
	if err != nil {
		return fmt.Errorf("connect to imap %s: %w", c.Server, err)
	}
	defer cl.Logout()

	if err := cl.Login(c.Username, c.Password); err != nil {
		return fmt.Errorf("imap login: %w", err)
	}

	if err := sendID(cl); err != nil {
		return fmt.Errorf("imap id: %w", err)
	}

	if _, err := cl.Select(c.Mailbox, false); err != nil {
		return fmt.Errorf("select mailbox %s: %w", c.Mailbox, err)
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uids...)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.SeenFlag}
	if err := cl.UidStore(seqSet, item, flags, nil); err != nil {
		return fmt.Errorf("mark seen: %w", err)
	}
	return nil
}

// idCommand is a raw IMAP ID command (RFC 2971).
type idCommand struct {
	params map[string]string
}

func (c *idCommand) Command() *imap.Command {
	return &imap.Command{
		Name:      "ID",
		Arguments: []interface{}{imap.FormatParamList(c.params)},
	}
}

// idHandler handles the untagged ID response.
type idHandler struct{}

func (h *idHandler) Handle(resp imap.Resp) error {
	// We don't need to parse the server's ID response.
	return nil
}

// sendID sends the IMAP ID command, required by some servers such as 163.com.
func sendID(cl *client.Client) error {
	cmd := &idCommand{
		params: map[string]string{
			"name":    "gacos-scraper",
			"version": "1.0",
			"vendor":  "github.com/xjock/gacos-scraper",
		},
	}
	_, err := cl.Execute(cmd, &idHandler{})
	return err
}
