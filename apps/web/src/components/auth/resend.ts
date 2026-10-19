/* One rhythm for every "we sent you an email" screen.
 *
 * Mirrors the server, which sends at most one mail a minute (and five an
 * hour) to any one address whoever asks, so a resend offered sooner would
 * only come back as "too many attempts". It should feel the same on every
 * screen that sends mail. */
export const RESEND_SECONDS = 60;

/** Mirrors `VerificationValidMinutes` on the server. */
export const EMAIL_VALID_MINUTES = 10;

/* Whether a fresh mail may be sent to this address right now.
 *
 * Both screens let you step back and change the address, and the countdown
 * belongs to the address it was started for — not to the screen. Without this
 * check, stepping back and submitting the same address again reissues the
 * code or link, and the server replaces the token, so the mail already in the
 * reader's inbox stops working while its replacement is still in flight.
 */
const canonical = (address: string) => address.trim().toLowerCase();

/* Compared canonically, because the server indexes codes by the lowercased
   address: `User@x.com` and `user@x.com` are one mailbox holding one token, so
   changing only the capitalisation would reissue it and break the mail already
   sitting in the inbox. */
export const canSendTo = (address: string, sentTo: string, countdown: number) =>
  countdown <= 0 || canonical(address) !== canonical(sentTo);
