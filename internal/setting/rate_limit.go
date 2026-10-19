package setting

var ModelRequestRateLimitEnabled = false
var ModelRequestRateLimitDurationMinutes = 1
var ModelRequestRateLimitCount = 0
var ModelRequestRateLimitSuccessCount = 1000

// Account rate limits. Every account gets the site-wide default; one that
// has never topped up gets the (usually lower) unpaid limit; an account's own
// override beats both. Zero means "no limit" at every level. They apply only
// while ModelRequestRateLimitEnabled is on. These replaced the spend tiers.
var (
	RateLimitDefaultRPM = 60
	RateLimitDefaultTPM = 100_000
	RateLimitUnpaidRPM  = 5
	RateLimitUnpaidTPM  = 5_000
)

// AccountRateLimit returns the RPM and TPM ceilings for one account.
func AccountRateLimit(hasToppedUp bool, rpmOverride, tpmOverride int) (rpm, tpm int) {
	rpm, tpm = RateLimitDefaultRPM, RateLimitDefaultTPM
	if !hasToppedUp {
		rpm, tpm = RateLimitUnpaidRPM, RateLimitUnpaidTPM
	}
	if rpmOverride > 0 {
		rpm = rpmOverride
	}
	if tpmOverride > 0 {
		tpm = tpmOverride
	}
	return rpm, tpm
}
