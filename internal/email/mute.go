package email

import (
	"context"
	"net/url"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// This file holds the transport-agnostic mute + List-Unsubscribe logic shared
// by the SES (*Sender) and SMTP (*SMTPSender) paths. Both transports must apply
// the same per-recipient mute suppression, CC filtering, and RFC 8058
// List-Unsubscribe headers; keeping the logic here (instead of duplicating it
// per transport) prevents the two paths from diverging.

// isRecipientMuted returns true when (email, scope) is muted. A nil checker or a
// store error is treated as "not muted" (fail-open) so a transient DB outage
// does not silently block approval emails.
func isRecipientMuted(ctx context.Context, mc MuteChecker, email, scope string) bool {
	if mc == nil {
		return false
	}
	muted, err := mc.IsNotificationMuted(ctx, email, scope)
	if err != nil {
		logging.Warnf("email: mute check failed for scope=%s: %v", scope, err)
		return false
	}
	return muted
}

// filterMutedRecipients returns a copy of addrs with any address muted for scope
// removed. The original slice is not modified. Errors from the mute store are
// treated as "not muted" (fail-open).
func filterMutedRecipients(ctx context.Context, mc MuteChecker, addrs []string, scope string) []string {
	if mc == nil || len(addrs) == 0 {
		return addrs
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if !isRecipientMuted(ctx, mc, addr, scope) {
			out = append(out, addr)
		}
	}
	return out
}

// unsubscribeURLFor constructs the one-click unsubscribe URL for the given
// (email, scope) pair. Returns "" when baseURL is empty or when no signing key
// is available (e.g. NOTIFICATION_MUTE_SECRET unset in production), so a
// tokenless, non-functional unsubscribe link is never emitted.
func unsubscribeURLFor(baseURL, email, scope string) string {
	if baseURL == "" {
		return ""
	}
	token := common.DeriveMuteToken(muteKey(), email, scope)
	if token == "" {
		return ""
	}
	q := url.Values{
		"token": {token},
		"email": {email},
		"scope": {scope},
	}
	return baseURL + "/api/notifications/unsubscribe?" + q.Encode()
}

// unsubscribeHeaderValuesFor returns the List-Unsubscribe and
// List-Unsubscribe-Post header values (RFC 8058) for the given (email, scope)
// pair. Returns ("", "") when baseURL is empty.
func unsubscribeHeaderValuesFor(baseURL, email, scope string) (headerValue, postValue string) {
	unsubURL := unsubscribeURLFor(baseURL, email, scope)
	if unsubURL == "" {
		return "", ""
	}
	return "<" + unsubURL + ">", "List-Unsubscribe=One-Click"
}
