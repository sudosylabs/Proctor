package smtp_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	mailsmtp "github.com/sudosylabs/proctor/packages/mail/smtp"
)

func TestSTARTTLSAuthentication(t *testing.T) {
	t.Parallel()

	serverTLS, roots := testCertificate(t)
	tests := []struct {
		name           string
		authentication mailsmtp.Authentication
		mechanism      string
	}{
		{name: "plain", authentication: mailsmtp.AuthenticationPlain, mechanism: "PLAIN"},
		{name: "login", authentication: mailsmtp.AuthenticationLogin, mechanism: "LOGIN"},
		{name: "auto prefers plain", authentication: mailsmtp.AuthenticationAuto, mechanism: "PLAIN"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			address, wait := startSMTPServer(t, func(connection net.Conn) error {
				return serveSecureSMTP(connection, serverTLS, test.mechanism, "user", "secret")
			})
			sender, err := mailsmtp.New(mailsmtp.Config{
				Address:         address,
				ServerName:      "localhost",
				Security:        mailsmtp.SecuritySTARTTLS,
				TLSConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
				Username:        "user",
				Password:        "secret",
				Authentication:  test.authentication,
				Timeout:         2 * time.Second,
				MessageIDDomain: "example.test",
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := sender.Test(t.Context()); err != nil {
				t.Fatalf("Test() error = %v", err)
			}
			wait()
		})
	}
}

func TestImplicitTLS(t *testing.T) {
	t.Parallel()

	serverTLS, roots := testCertificate(t)
	address, wait := startSMTPServer(t, func(connection net.Conn) error {
		tlsConnection := tls.Server(connection, serverTLS.Clone())
		if err := tlsConnection.Handshake(); err != nil {
			return err
		}
		return serveSMTP(tlsConnection, nil)
	})
	sender, err := mailsmtp.New(mailsmtp.Config{
		Address:         address,
		ServerName:      "localhost",
		Security:        mailsmtp.SecurityTLS,
		TLSConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		Timeout:         2 * time.Second,
		MessageIDDomain: "example.test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := sender.Test(t.Context()); err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	wait()
}

func serveSecureSMTP(
	connection net.Conn,
	serverTLS *tls.Config,
	mechanism, username, password string,
) error {
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	if err := writeSMTP(writer, "220 test.example ESMTP ready"); err != nil {
		return err
	}
	command, err := readSMTP(reader)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(command, "EHLO ") {
		return fmt.Errorf("expected EHLO, got %q", command)
	}
	if err := writeSMTP(writer, "250-test.example", "250 STARTTLS"); err != nil {
		return err
	}
	command, err = readSMTP(reader)
	if err != nil {
		return err
	}
	if command != "STARTTLS" {
		return fmt.Errorf("expected STARTTLS, got %q", command)
	}
	if err := writeSMTP(writer, "220 2.0.0 ready for TLS"); err != nil {
		return err
	}

	tlsConnection := tls.Server(connection, serverTLS.Clone())
	if err := tlsConnection.Handshake(); err != nil {
		return err
	}
	reader = bufio.NewReader(tlsConnection)
	writer = bufio.NewWriter(tlsConnection)
	command, err = readSMTP(reader)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(command, "EHLO ") {
		return fmt.Errorf("expected EHLO after STARTTLS, got %q", command)
	}
	if err := writeSMTP(writer, "250-test.example", "250 AUTH PLAIN LOGIN"); err != nil {
		return err
	}

	command, err = readSMTP(reader)
	if err != nil {
		return err
	}
	switch mechanism {
	case "PLAIN":
		fields := strings.Fields(command)
		if len(fields) != 3 || fields[0] != "AUTH" || fields[1] != "PLAIN" {
			return fmt.Errorf("unexpected PLAIN command %q", command)
		}
		credentials, decodeErr := base64.StdEncoding.DecodeString(fields[2])
		if decodeErr != nil {
			return decodeErr
		}
		if string(credentials) != "\x00"+username+"\x00"+password {
			return fmt.Errorf("unexpected PLAIN credentials")
		}
	case "LOGIN":
		if command != "AUTH LOGIN" {
			return fmt.Errorf("unexpected LOGIN command %q", command)
		}
		if err := writeSMTP(writer, "334 "+base64.StdEncoding.EncodeToString([]byte("Username:"))); err != nil {
			return err
		}
		encodedUsername, readErr := readSMTP(reader)
		if readErr != nil {
			return readErr
		}
		if decodedString(encodedUsername) != username {
			return fmt.Errorf("unexpected LOGIN username")
		}
		if err := writeSMTP(writer, "334 "+base64.StdEncoding.EncodeToString([]byte("Password:"))); err != nil {
			return err
		}
		encodedPassword, readErr := readSMTP(reader)
		if readErr != nil {
			return readErr
		}
		if decodedString(encodedPassword) != password {
			return fmt.Errorf("unexpected LOGIN password")
		}
	default:
		return fmt.Errorf("unsupported test mechanism %q", mechanism)
	}
	if err := writeSMTP(writer, "235 2.7.0 authenticated"); err != nil {
		return err
	}
	command, err = readSMTP(reader)
	if err != nil {
		return err
	}
	if command != "QUIT" {
		return fmt.Errorf("expected QUIT, got %q", command)
	}
	return writeSMTP(writer, "221 2.0.0 bye")
}

func readSMTP(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), err
}

func writeSMTP(writer *bufio.Writer, lines ...string) error {
	for _, line := range lines {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func decodedString(encoded string) string {
	decoded, _ := base64.StdEncoding.DecodeString(encoded)
	return string(decoded)
}

func testCertificate(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	parsedCertificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsedCertificate)
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}, roots
}
