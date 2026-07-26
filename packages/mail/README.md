# Proctor Mail

`mail` is a reusable Go module for composing and sending transactional email.
It has no dependency on the Proctor server and no third-party Go dependencies.

Module:

```text
github.com/sudosylabs/proctor/packages/mail
```

## Scope

The package owns:

- a transport-neutral message model;
- strict address, header, attachment, and size validation;
- RFC 5322 and MIME composition;
- plain text and HTML alternatives;
- regular and CID-referenced inline attachments;
- SMTP with cleartext development mode, required STARTTLS, or implicit TLS;
- PLAIN and LOGIN authentication over encrypted connections;
- context cancellation and bounded connection lifetimes;
- an in-memory sender and reusable conformance suite.

The package deliberately does not own application templates, localization,
queues, retry policy, rate limiting, provider webhooks, or school-specific
email workflows. Those policies belong in the server or in future independent
packages.

## Message example

```go
message := mail.Message{
    From: mail.Address{
        Name:    "Example School",
        Address: "noreply@school.example",
    },
    To: []mail.Address{{
        Name:    "Student",
        Address: "student@example.com",
    }},
    ReplyTo: []mail.Address{{
        Address: "support@school.example",
    }},
    Subject: "Your exam is ready",
    Text:    "Your exam is ready. Sign in to begin.",
    HTML:    "<p>Your exam is ready. <a href=\"...\">Sign in to begin</a>.</p>",
    Headers: map[string][]string{
        "Auto-Submitted": {"auto-generated"},
    },
}

receipt, err := sender.Send(ctx, message)
```

Applications should supply a meaningful text alternative whenever they send
HTML. The package does not attempt lossy HTML-to-text conversion.

`BCC` recipients are included in the SMTP envelope and intentionally omitted
from composed headers. Duplicate envelope recipients are removed while
preserving their first occurrence.

## In-memory sender

```go
sender, err := memory.New(mail.ComposerConfig{
    MessageIDDomain: "school.example",
})
if err != nil {
    return err
}

_, err = sender.Send(ctx, message)
deliveries := sender.Deliveries()
```

The in-memory sender stores fully composed messages. `Deliveries` returns deep
copies, making it safe for concurrent tests.

## SMTP sender

```go
sender, err := smtp.New(smtp.Config{
    Address:         "smtp.school.example:587",
    ServerName:      "smtp.school.example",
    Security:        smtp.SecuritySTARTTLS,
    Username:        username,
    Password:        password,
    Authentication:  smtp.AuthenticationAuto,
    Timeout:         10 * time.Second,
    MessageIDDomain: "school.example",
})
if err != nil {
    return err
}

if err := sender.Test(ctx); err != nil {
    return err
}
_, err = sender.Send(ctx, message)
```

`smtp.Sender` opens one connection per operation. Connection pooling,
background delivery, retries, and dead-letter handling have operational
semantics that should be implemented by a separate queue or delivery service.

The sender supports:

- `smtp.SecurityNone` for unauthenticated local development servers;
- `smtp.SecuritySTARTTLS`, which fails if STARTTLS is not advertised;
- `smtp.SecurityTLS` for implicit TLS;
- `smtp.AuthenticationAuto`, `Plain`, `Login`, or `None`.

Credentials are rejected when transport security is disabled. Certificate
verification is enabled by default. A custom `tls.Config` may be supplied for
private school certificate authorities.

## Attachments

Attachments use byte slices in the initial API so message size is known and
bounded before network delivery:

```go
message.Attachments = []mail.Attachment{
    {
        Filename:    "instructions.pdf",
        ContentType: "application/pdf",
        Data:        pdfBytes,
    },
    {
        Filename:    "logo.png",
        ContentType: "image/png",
        ContentID:   "school-logo",
        Inline:      true,
        Data:        logoBytes,
    },
}
message.HTML = `<img src="cid:school-logo" alt="School logo">`
```

Inline attachments require HTML and a unique content ID. Filenames may not
contain paths. Binary data is base64 encoded with RFC-compatible line lengths.

## Headers and threading

`Message.Headers` accepts safe custom ASCII headers. Headers managed by the
composer—such as `From`, `To`, `Subject`, `Date`, `Message-ID`, and MIME
headers—cannot be overridden.

Threading uses complete message IDs:

```go
message.MessageID = "<notification-123@school.example>"
message.InReplyTo = "<previous@school.example>"
message.References = []string{
    "<root@school.example>",
    "<previous@school.example>",
}
```

When `MessageID` or `Date` is absent, the composer generates it.

## Limits and SMTPUTF8

The defaults are 25 MiB per encoded message and 100 unique recipients. Both
limits can be lowered through `ComposerConfig` or `smtp.Config`.

Envelope addresses are ASCII-only in v1. This avoids silently sending invalid
internationalized addresses without SMTPUTF8 negotiation. Display names,
subjects, and body content may use UTF-8 and are encoded appropriately.

## Testing

Run unit, race, and vet checks:

```bash
make check
```

Run the complete sender conformance suite against a real Mailpit SMTP server:

```bash
make conformance-smtp
```

The target starts a pinned Mailpit container, waits for its built-in health
check, runs the tests, and removes the container and network even when tests
fail. SMTP is available only at `127.0.0.1:11025`; the debugging UI is at
`http://127.0.0.1:18025`.

For interactive debugging:

```bash
make smtp-up
MAIL_SMTP_ADDRESS=127.0.0.1:11025 go test ./smtp -run Integration -count=1
make smtp-down
```

Ports and the image are configurable:

```bash
MAIL_SMTP_PORT=21025 \
MAIL_HTTP_PORT=28025 \
MAILPIT_IMAGE=axllent/mailpit:v1.30 \
make conformance-smtp
```

Backend authors can use `mailtest.Run` to validate another sender.

## Compatibility

The module follows semantic versioning once tagged. Before `v1.0.0`, exported
APIs may change as the Proctor server exercises the package in production-like
workloads.

## License

Apache License 2.0. See [LICENSE](LICENSE).
