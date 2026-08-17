// Package mail composes and sends transactional email through interchangeable
// transports.
//
// Failed sends have portable temporary, permanent, or acceptance-uncertain
// outcomes while retaining errors.Is compatibility with the package's
// transport-neutral errors. Application templates, localization, queues, and
// rate limiting deliberately remain outside this package.
package mail
