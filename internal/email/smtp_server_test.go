package email

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSMTPServer is a simple mock SMTP server for testing
type mockSMTPServer struct {
	listener    net.Listener
	port        int
	authFail    bool
	wg          sync.WaitGroup
	receivedMsg string
	mu          sync.Mutex
	inData      bool
}

// newMockSMTPServer creates a new mock SMTP server
func newMockSMTPServer(t *testing.T, authFail bool) *mockSMTPServer {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().(*net.TCPAddr)

	server := &mockSMTPServer{
		listener: listener,
		port:     addr.Port,
		authFail: authFail,
	}

	return server
}

// start begins accepting connections
func (s *mockSMTPServer) start(t *testing.T) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		conn, err := s.listener.Accept()
		if err != nil {
			return // Server was closed
		}
		defer conn.Close()

		// Set a deadline to prevent hanging
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)

		// Send greeting
		fmt.Fprintf(writer, "220 localhost SMTP Test Server\r\n")
		writer.Flush()

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}

			line = strings.TrimSpace(line)

			// If in DATA mode, collect the message until we see "."
			if s.inData {
				s.mu.Lock()
				s.receivedMsg += line + "\n"
				s.mu.Unlock()
				if line == "." {
					s.inData = false
					fmt.Fprintf(writer, "250 2.0.0 OK: message queued\r\n")
					writer.Flush()
				}
				continue
			}

			s.mu.Lock()
			s.receivedMsg += line + "\n"
			s.mu.Unlock()

			// Determine response based on command
			switch {
			case strings.HasPrefix(line, "EHLO") || strings.HasPrefix(line, "HELO"):
				// Send multi-line EHLO response
				fmt.Fprintf(writer, "250-localhost Hello\r\n")
				fmt.Fprintf(writer, "250-SIZE 35882577\r\n")
				fmt.Fprintf(writer, "250-8BITMIME\r\n")
				fmt.Fprintf(writer, "250-AUTH PLAIN LOGIN\r\n")
				fmt.Fprintf(writer, "250 OK\r\n")
				writer.Flush()
			case strings.HasPrefix(line, "AUTH"):
				if s.authFail {
					fmt.Fprintf(writer, "535 5.7.8 Authentication failed\r\n")
				} else {
					fmt.Fprintf(writer, "235 2.7.0 Authentication successful\r\n")
				}
				writer.Flush()
			case strings.HasPrefix(line, "MAIL FROM"):
				fmt.Fprintf(writer, "250 2.1.0 OK\r\n")
				writer.Flush()
			case strings.HasPrefix(line, "RCPT TO"):
				fmt.Fprintf(writer, "250 2.1.5 OK\r\n")
				writer.Flush()
			case strings.HasPrefix(line, "DATA"):
				s.inData = true
				fmt.Fprintf(writer, "354 Start mail input; end with <CRLF>.<CRLF>\r\n")
				writer.Flush()
			case strings.HasPrefix(line, "QUIT"):
				fmt.Fprintf(writer, "221 2.0.0 Bye\r\n")
				writer.Flush()
				return
			case strings.HasPrefix(line, "RSET"):
				fmt.Fprintf(writer, "250 2.0.0 OK\r\n")
				writer.Flush()
			default:
				fmt.Fprintf(writer, "250 OK\r\n")
				writer.Flush()
			}
		}
	}()
}

// stop closes the server
func (s *mockSMTPServer) stop() {
	s.listener.Close()
	s.wg.Wait()
}

// TestSMTPSender_SendToEmail_WithMockServer tests with a simple mock SMTP server (no TLS)
func TestSMTPSender_SendToEmail_WithMockServer_NoTLS(t *testing.T) {
	// Create mock server
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "sender@test.com",
		fromName:  "",
		useTLS:    false,
	}

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@test.com", "Test Subject", "Test Body")

	// This should succeed with the mock server
	require.NoError(t, err)
}

// TestSMTPSender_SendToEmail_WithMockServer_WithAuth tests with auth (no TLS)
func TestSMTPSender_SendToEmail_WithMockServer_WithAuth(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "testuser",
		password:  "testpass",
		fromEmail: "sender@test.com",
		fromName:  "Sender Name",
		useTLS:    false,
	}

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@test.com", "Test Subject", "Test Body")

	require.NoError(t, err)
}

// TestSMTPSender_SendToEmail_WithMockServer_AuthFailure tests auth failure
func TestSMTPSender_SendToEmail_WithMockServer_AuthFailure(t *testing.T) {
	// Create server that rejects auth
	server := newMockSMTPServer(t, true)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "baduser",
		password:  "badpass",
		fromEmail: "sender@test.com",
		fromName:  "",
		useTLS:    false,
	}

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@test.com", "Test Subject", "Test Body")

	require.Error(t, err)
	// The error should indicate auth failure
	assert.Contains(t, err.Error(), "failed to send email via SMTP")
}

// TestSMTPSender_SendPasswordResetEmail_WithMockServer tests the full flow
func TestSMTPSender_SendPasswordResetEmail_WithMockServer(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "noreply@cudly.io",
		fromName:  "CUDly",
		useTLS:    false,
	}

	ctx := context.Background()
	err := sender.SendPasswordResetEmail(ctx, "user@example.com", "https://example.com/reset?token=abc123")

	require.NoError(t, err)
}

// TestSMTPSender_SendWelcomeEmail_WithMockServer tests welcome email
func TestSMTPSender_SendWelcomeEmail_WithMockServer(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "noreply@cudly.io",
		fromName:  "CUDly",
		useTLS:    false,
	}

	ctx := context.Background()
	err := sender.SendWelcomeEmail(ctx, "newuser@example.com", "https://dashboard.example.com", "admin")

	require.NoError(t, err)
}

// TestSMTPSender_SendNewRecommendationsNotification_WithMockServer tests recommendations email
func TestSMTPSender_SendNewRecommendationsNotification_WithMockServer(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "notifications@cudly.io",
		fromName:  "CUDly Notifications",
		useTLS:    false,
	}

	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     1500.00,
		TotalUpfrontCost: 6000.00,
		Recommendations: []RecommendationSummary{
			{Service: "rds", ResourceType: "db.r5.large", Engine: "postgres", Region: "us-east-1", Count: 3, MonthlySavings: 500.0},
		},
	}

	ctx := context.Background()
	err := sender.SendNewRecommendationsNotification(ctx, data)

	require.NoError(t, err)
}

// TestSMTPSender_SendScheduledPurchaseNotification_WithMockServer tests scheduled purchase email
func TestSMTPSender_SendScheduledPurchaseNotification_WithMockServer(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "notifications@cudly.io",
		fromName:  "CUDly",
		useTLS:    false,
	}

	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "token-xyz",
		TotalSavings:      2000.00,
		TotalUpfrontCost:  8000.00,
		PurchaseDate:      "March 15, 2024",
		DaysUntilPurchase: 7,
		PlanName:          "Production Plan",
		Recommendations: []RecommendationSummary{
			{Service: "rds", ResourceType: "db.m5.large", Engine: "mysql", Region: "eu-west-1", Count: 5, MonthlySavings: 400.0},
		},
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)

	require.NoError(t, err)
}

// TestSMTPSender_SendPurchaseConfirmation_WithMockServer tests purchase confirmation email
func TestSMTPSender_SendPurchaseConfirmation_WithMockServer(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "notifications@cudly.io",
		fromName:  "CUDly",
		useTLS:    false,
	}

	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     3000.00,
		TotalUpfrontCost: 12000.00,
		Recommendations: []RecommendationSummary{
			{Service: "elasticache", ResourceType: "cache.r5.large", Engine: "redis", Region: "us-east-1", Count: 4, MonthlySavings: 750.0},
		},
	}

	ctx := context.Background()
	err := sender.SendPurchaseConfirmation(ctx, data)

	require.NoError(t, err)
}

// TestSMTPSender_SendPurchaseFailedNotification_WithMockServer tests purchase failed email
func TestSMTPSender_SendPurchaseFailedNotification_WithMockServer(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "notifications@cudly.io",
		fromName:  "CUDly",
		useTLS:    false,
	}

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		Recommendations: []RecommendationSummary{
			{Service: "opensearch", ResourceType: "r5.large.search", Engine: "", Region: "us-west-2", Count: 2},
		},
	}

	ctx := context.Background()
	err := sender.SendPurchaseFailedNotification(ctx, data)

	require.NoError(t, err)
}

// TestSMTPSender_SendToEmail_WithMockServer_MultipleRecipients tests multiple recipients
func TestSMTPSender_SendToEmail_WithMockServer_MessageContent(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "sender@test.com",
		fromName:  "Test Sender",
		useTLS:    false,
	}

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@test.com", "Subject With Special Chars: <>&\"'", "Body with UTF-8: Hola Mundo")

	require.NoError(t, err)
}

// TestSMTPSender_SendToEmail_WithMockServer_LongContent tests long content
func TestSMTPSender_SendToEmail_WithMockServer_LongContent(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		username:  "",
		password:  "",
		fromEmail: "sender@test.com",
		fromName:  "Long Content Test",
		useTLS:    false,
	}

	// Create long body
	longBody := ""
	for i := 0; i < 50; i++ {
		longBody += fmt.Sprintf("This is line %d of the email body with additional content.\r\n", i+1)
	}

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@test.com", "Long Email Test", longBody)

	require.NoError(t, err)
}

// --- Direct sendMailTLS tests with flexible error behaviors ---

// startFlexSMTPServer starts a mock SMTP server with configurable failure behavior.
func startFlexSMTPServer(t *testing.T, behavior string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleFlexSMTPConn(conn, behavior)
		}
	}()

	cleanup := func() {
		listener.Close()
		<-done
	}

	return addr, cleanup
}

func handleFlexSMTPConn(conn net.Conn, behavior string) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)

	fmt.Fprintf(conn, "220 localhost SMTP Test Server\r\n")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))

		switch {
		case strings.HasPrefix(cmd, "EHLO") || strings.HasPrefix(cmd, "HELO"):
			fmt.Fprintf(conn, "250-localhost Hello\r\n")
			fmt.Fprintf(conn, "250-SIZE 10240000\r\n")
			fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")

		case strings.HasPrefix(cmd, "STARTTLS"):
			fmt.Fprintf(conn, "220 Ready to start TLS\r\n")
			return // plaintext conn -> client TLS handshake fails

		case strings.HasPrefix(cmd, "AUTH"):
			if behavior == "auth_fail_535" {
				fmt.Fprintf(conn, "535 5.7.8 Authentication credentials invalid\r\n")
			} else if behavior == "auth_fail_other" {
				fmt.Fprintf(conn, "454 4.7.0 Temporary authentication failure\r\n")
			} else {
				fmt.Fprintf(conn, "235 2.7.0 Authentication successful\r\n")
			}

		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if behavior == "mail_fail" {
				fmt.Fprintf(conn, "550 5.1.0 Sender rejected\r\n")
			} else {
				fmt.Fprintf(conn, "250 2.1.0 OK\r\n")
			}

		case strings.HasPrefix(cmd, "RCPT TO:"):
			if behavior == "rcpt_fail" {
				fmt.Fprintf(conn, "550 5.1.1 Recipient rejected\r\n")
			} else {
				fmt.Fprintf(conn, "250 2.1.5 OK\r\n")
			}

		case strings.HasPrefix(cmd, "DATA"):
			if behavior == "data_fail" {
				fmt.Fprintf(conn, "554 5.0.0 Transaction failed\r\n")
			} else {
				fmt.Fprintf(conn, "354 Start mail input; end with <CRLF>.<CRLF>\r\n")
				for {
					dataLine, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimSpace(dataLine) == "." {
						break
					}
				}
				fmt.Fprintf(conn, "250 2.0.0 OK\r\n")
			}

		case strings.HasPrefix(cmd, "QUIT"):
			fmt.Fprintf(conn, "221 2.0.0 Bye\r\n")
			return

		default:
			fmt.Fprintf(conn, "500 5.5.1 Command not recognized\r\n")
		}
	}
}

// testFlexAuth implements smtp.Auth for testing without TLS requirement.
type testFlexAuth struct {
	username string
	password string
}

func (a *testFlexAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	resp := []byte("\x00" + a.username + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (a *testFlexAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("unexpected server challenge")
	}
	return nil, nil
}

func TestSendMailTLS_Success_NoTLS_NoAuth(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "success")
	defer cleanup()

	sender := &SMTPSender{useTLS: false}
	err := sender.sendMailTLS(addr, nil, "from@test.com", []string{"to@test.com"}, []byte("Subject: Test\r\n\r\nBody"))
	assert.NoError(t, err)
}

func TestSendMailTLS_Success_NoTLS_WithAuth(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "success")
	defer cleanup()

	sender := &SMTPSender{useTLS: false}
	auth := &testFlexAuth{username: "user", password: "pass"}
	err := sender.sendMailTLS(addr, auth, "from@test.com", []string{"to@test.com"}, []byte("Subject: Test\r\n\r\nBody"))
	assert.NoError(t, err)
}

func TestSendMailTLS_MultipleRecipients(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "success")
	defer cleanup()

	sender := &SMTPSender{useTLS: false}
	err := sender.sendMailTLS(addr, nil, "from@test.com", []string{"to1@test.com", "to2@test.com", "to3@test.com"}, []byte("Subject: Multi\r\n\r\nBody"))
	assert.NoError(t, err)
}

func TestSendMailTLS_DialFail(t *testing.T) {
	sender := &SMTPSender{useTLS: false}
	err := sender.sendMailTLS("127.0.0.1:1", nil, "from@test.com", []string{"to@test.com"}, []byte("test"))
	require.Error(t, err)
}

func TestSendMailTLS_StartTLSFail(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "success")
	defer cleanup()

	host, _, _ := net.SplitHostPort(addr)
	sender := &SMTPSender{host: host, useTLS: true}
	err := sender.sendMailTLS(addr, nil, "from@test.com", []string{"to@test.com"}, []byte("test"))
	require.Error(t, err)
}

func TestSendMailTLS_Auth535Error(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "auth_fail_535")
	defer cleanup()

	sender := &SMTPSender{useTLS: false}
	auth := &testFlexAuth{username: "user", password: "pass"}
	err := sender.sendMailTLS(addr, auth, "from@test.com", []string{"to@test.com"}, []byte("test"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SMTP authentication failed - check username/password")
}

func TestSendMailTLS_AuthOtherError(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "auth_fail_other")
	defer cleanup()

	sender := &SMTPSender{useTLS: false}
	auth := &testFlexAuth{username: "user", password: "pass"}
	err := sender.sendMailTLS(addr, auth, "from@test.com", []string{"to@test.com"}, []byte("test"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SMTP authentication failed - check username/password")
}

func TestSendMailTLS_MailFromFail(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "mail_fail")
	defer cleanup()

	sender := &SMTPSender{useTLS: false}
	err := sender.sendMailTLS(addr, nil, "from@test.com", []string{"to@test.com"}, []byte("test"))
	require.Error(t, err)
}

func TestSendMailTLS_RcptToFail(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "rcpt_fail")
	defer cleanup()

	sender := &SMTPSender{useTLS: false}
	err := sender.sendMailTLS(addr, nil, "from@test.com", []string{"to@test.com"}, []byte("test"))
	require.Error(t, err)
}

func TestSendMailTLS_DataFail(t *testing.T) {
	addr, cleanup := startFlexSMTPServer(t, "data_fail")
	defer cleanup()

	sender := &SMTPSender{useTLS: false}
	err := sender.sendMailTLS(addr, nil, "from@test.com", []string{"to@test.com"}, []byte("test"))
	require.Error(t, err)
}
