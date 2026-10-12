package mailer

import (
	"bufio"
	"context"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"testing"
)

func TestSMTPConfigValidation(t *testing.T) {
	ok := SMTPConfig{Host: "smtp.example.com", Port: 465, User: "u", Pass: "p", From: "AI Workforce OS <no-reply@example.com>", TLS: "tls"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, mut := range map[string]func(*SMTPConfig){
		"no host":         func(c *SMTPConfig) { c.Host = "" },
		"bad port":        func(c *SMTPConfig) { c.Port = 0 },
		"bad from":        func(c *SMTPConfig) { c.From = "not an address" },
		"bad tls mode":    func(c *SMTPConfig) { c.TLS = "maybe" },
		"user without pw": func(c *SMTPConfig) { c.Pass = "" },
	} {
		c := ok
		mut(&c)
		if c.Validate() == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
	if _, err := NewSMTP(SMTPConfig{}); err == nil {
		t.Error("NewSMTP must validate")
	}
}

func TestBuildProducesASafeMultipartMessage(t *testing.T) {
	from, _ := mail.ParseAddress("AI Workforce OS <no-reply@example.com>")
	to, _ := mail.ParseAddress("ana@example.com")
	raw, err := build(from, to, Message{To: "ana@example.com", Subject: "Confirma tu correo ñ", Text: "Hola\nenlace: https://app.example.com/verify?token=abc", HTML: "<p>Hola ñ</p>"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("not a valid RFC 5322 message: %v", err)
	}
	subj, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if subj != "Confirma tu correo ñ" {
		t.Errorf("subject = %q", subj)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" || params["boundary"] == "" {
		t.Errorf("content type = %q %v %v", mt, params, err)
	}
	if !strings.Contains(string(raw), "text/plain") || !strings.Contains(string(raw), "text/html") {
		t.Error("both alternatives expected")
	}
	if msg.Header.Get("Message-Id") == "" || msg.Header.Get("Date") == "" {
		t.Error("Message-ID and Date expected")
	}
}

func TestSendRejectsHeaderInjectionBeforeAnyNetworkCall(t *testing.T) {
	s := &SMTP{cfg: SMTPConfig{Host: "127.0.0.1", Port: 1, From: "no-reply@example.com", TLS: "none"}}
	for _, m := range []Message{
		{To: "a@example.com\r\nBcc: victim@example.com", Subject: "x", Text: "x"},
		{To: "a@example.com", Subject: "x\r\nBcc: victim@example.com", Text: "x"},
		{To: "not-an-address", Subject: "x", Text: "x"},
	} {
		err := s.Send(context.Background(), m)
		if err == nil || strings.Contains(err.Error(), "connect") {
			t.Errorf("message %+v: want a validation error before connecting, got %v", m, err)
		}
	}
}

// fakeSMTP is a minimal SMTP server (EHLO, AUTH PLAIN, MAIL, RCPT, DATA, QUIT) that records what it receives.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		say := func(s string) { fmt.Fprintf(c, "%s\r\n", s) }
		var log strings.Builder
		say("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			log.WriteString(line + "\n")
			switch cmd := strings.ToUpper(strings.Fields(line + " x")[0]); cmd {
			case "EHLO", "HELO":
				say("250-fake")
				say("250 AUTH PLAIN")
			case "AUTH":
				say("235 ok")
			case "MAIL", "RCPT":
				say("250 ok")
			case "DATA":
				say("354 go")
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					log.WriteString(l)
				}
				say("250 queued")
			case "QUIT":
				say("221 bye")
				got <- log.String()
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPDeliversEndToEnd(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	s, err := NewSMTP(SMTPConfig{Host: host, Port: port, User: "u", Pass: "p", From: "AI Workforce OS <no-reply@example.com>", TLS: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Message{To: "ana@example.com", Subject: "Confirma tu correo", Text: "enlace: https://app.example.com/verify?token=abc", HTML: "<p>hola</p>"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	log := <-got
	for _, want := range []string{"MAIL FROM:<no-reply@example.com>", "RCPT TO:<ana@example.com>", "AUTH PLAIN", "multipart/alternative", "verify?token=3Dabc"} {
		if !strings.Contains(log, want) {
			t.Errorf("server log is missing %q:\n%s", want, log)
		}
	}
}
