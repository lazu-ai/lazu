<!-- Lazu · Privacy Policy | Lazu · https://lazu.ai/privacy -->

# Privacy Policy

Last updated: October 2, 2026

Lazu is an AI model API aggregation, routing, usage, and billing service provided and operated by DUBRIO LLC ("DUBRIO," "we," "us," or "our"). This Privacy Policy explains what information we process when you visit `https://lazu.ai`, create a Lazu account, use Lazu APIs or console features, or contact us.

By creating an account or using the service, you acknowledge the practices described in this policy. Where consent is required by applicable law, we will ask for it separately.

## 1. Information We Process

### 1.1 Account and authentication information

- For password-based accounts, we process your username, display name, email address, and a one-way password hash. We do not store your plaintext password.
- If you choose Google Sign-In, we request only the `openid`, `email`, and `profile` scopes. We receive your stable Google account identifier, name, and verified email address. We do not request access to Gmail, Google Drive, Google Contacts, calendars, or other Google product content.
- If you choose GitHub Sign-In, we receive your stable GitHub account identifier, username or display name, and a verified email address.
- We store the external provider name, provider account identifier, and verified email needed to create, link, secure, and recover your Lazu account. We do not retain Google or GitHub access tokens after the sign-in flow is complete.

### 1.2 API usage and operational information

We process records needed to operate and bill the service, such as account and API key identifiers, request identifiers, selected model and provider, timestamps, token or unit usage, price and credit entries, latency, status and error information, IP address, approximate country, and client framework or version.

Your API inputs, uploaded content, and model outputs are processed to provide the service and may be sent to the model provider selected by you or by the routing configuration. We also record the content of API requests and responses in request logs, kept as described in Retention and Deletion. Lazu does not use your inputs or outputs to train models and does not sell them for advertising. Model providers process content under their own terms and data policies, which can vary by provider and route.

If you use a feature that intentionally stores content, such as the Files API, we store the file and related metadata until you delete it or the applicable retention rule removes it. Support and security investigations may also require temporary access to information you choose to provide.

### 1.3 Payment and transaction information

Card payments are handled by Stripe Checkout. We process transaction records such as the Lazu order reference, Stripe customer identifier, amount, currency, payment status, payment method type, receipt email, credits purchased, and credits consumed. Complete card numbers and card security codes are collected by Stripe and do not pass through or remain on Lazu servers.

If Lazu later displays a stablecoin or other onchain payment option and you choose it, we may process the wallet address, blockchain network, asset, amount, transaction hash, confirmation and payment status, provider or order references, and fraud or compliance screening results needed to complete and reconcile the purchase. Blockchain records are public and generally immutable, and payment processors, blockchain networks, explorers, or analytics providers may process them independently. Lazu will never ask for or store your private key or seed phrase. A wallet payment does not by itself verify the payer's legal identity. This paragraph applies only when an onchain option is actually shown at checkout; Lazu does not currently represent that such an option is available.

### 1.4 Device, cookie, and communication information

We process technical information such as browser and device characteristics, language, time zone, cookie or local-storage identifiers, and security logs. If you contact us, we process the message, contact details, and related support history needed to respond.

When you first arrive, we store where you came from (campaign parameters such as utm_source, the referring site and the page you landed on) in a first-party cookie for up to 30 days. If you create an account, we keep that record, your sign-up method and your country, but not your IP address, to understand which channels bring people to Lazu.

We also use a random first-party browser identifier, stored for up to 30 days, to connect tagged promotional visits to a later registration. When you open a link with UTM parameters or a referral code, we record the campaign fields, referral code, landing path (without its query string or fragment), referring hostname and server timestamp, even if you do not register. These visit records are retained for 90 days and do not include your IP address or full referring URL. The first registration source remains unchanged by later campaigns; referral rewards use the most recent valid referral code. Account deletion removes the associated attribution records.

After you sign up we may ask where you heard about Lazu (for example an AI assistant, a search engine or a community). You can skip it; your answer is stored with the sign-up source above and deleted with your account.

### 1.5 Lazu Studio works

When you create images or videos in Lazu Studio, we store your prompts, the models and settings you chose, any reference images you add, the resulting files and the amount each one was charged. Works are visible only to you by default. If you ask to publish a work and it passes review, the work, its prompt, model and settings appear on public pages that search engines may index, together with a title and categories written by our text model; you can unpublish it at any time.

To prevent abuse, we record your IP address when you download an original image and when you claim the daily free image. When you search in Studio, we record the search terms to improve search and related suggestions.

## 2. How We Use Information

We use information to:

1. create, authenticate, link, secure, and administer accounts;
2. route model requests and return responses;
3. meter usage, maintain balances, process payments, and issue receipts;
4. provide files, console, support, and account-recovery features;
5. detect fraud, abuse, attacks, and violations of our terms;
6. diagnose incidents and improve reliability and performance;
7. communicate service, security, billing, and policy updates; and
8. comply with applicable law and enforce our agreements.

We do not use Google user data, payment information, or API content to determine creditworthiness or to deliver personalized advertising.

## 3. How We Share Information

We do not sell personal information. We disclose only what is reasonably necessary to:

1. **Model providers:** transmit requests and receive outputs for the model or route you use.
2. **Identity providers:** complete sign-in and account-linking flows with Google, GitHub, or an administrator-configured enterprise identity provider.
3. **Payment providers:** let Stripe process checkout, confirm payment, prevent fraud, and provide receipts and, if an onchain option is later enabled, let the disclosed crypto payment, blockchain, and compliance providers complete and reconcile that payment.
4. **Infrastructure and service providers:** operate hosting, databases, object storage, email delivery, monitoring, and customer support under appropriate confidentiality and security obligations.
5. **Authorities and rights protection:** comply with valid legal process or protect users, DUBRIO, and the public from fraud, abuse, or security threats.
6. **Business transfers:** support a merger, financing, reorganization, or sale, subject to applicable notice and protection requirements.

## 4. Google API Data

Lazu's use and transfer of information received from Google APIs adheres to the [Google API Services User Data Policy](https://developers.google.com/terms/api-services-user-data-policy), including its Limited Use requirements.

Google account information is used only for user-facing authentication, account linking, account security, and support requested by the user. We do not sell Google user data, use it for advertising, or transfer it to data brokers. You can revoke Lazu's Google access from your Google Account permissions. Revocation prevents future Google Sign-In but does not automatically delete the Lazu account or records we must retain; you may request deletion as described below.

## 5. Retention and Deletion

- Request logs, which include the content of API requests and responses, are retained for 180 days so that we can look into a specific request with you, investigate incidents and prevent abuse, and are then deleted.
- Usage records (request identifiers, model, token usage, cost, status and timing, without request or response content) are retained for 90 days.
- Account, identity, balance, ledger, payment, security, and audit records are retained while the account is active and afterward as reasonably necessary for account closure, fraud prevention, dispute resolution, accounting, tax, and legal obligations.
- If an onchain payment option is enabled, related transaction hashes and reconciliation records may be retained for the same purposes. Information written to a public blockchain cannot be deleted or changed by Lazu.
- Stored files remain until deleted by the user or removed under the applicable product retention rule.
- Files of Lazu Studio works that are not public are kept for 180 days, then deleted; we remind you to download them 7 days before. Making a work private again restarts the 180 days. Public works are kept while they stay public.
- Lazu Studio download and daily free-image records, including IP addresses, are kept for 90 days; Studio search terms are kept for 90 days.
- When information is no longer needed, we delete or de-identify it, unless retention is required or permitted by law.

To request account and personal-data deletion, contact `support@lazu.ai`. We may need to verify that you control the account. Deletion does not affect data already processed independently by model, identity, or payment providers under their own policies, or records on a public blockchain that Lazu cannot erase.

## 6. Your Choices and Rights

Depending on applicable law, you may request access, correction, deletion, restriction, portability, or withdrawal of consent, and may object to certain processing. You may update available profile information in the console, revoke third-party sign-in through the provider, stop using an API key, delete stored files, or request account deletion.

## 7. International Processing

Lazu and its service providers may process information in the United States and other countries where our providers operate. Those locations may have different data-protection laws. We use reasonable contractual, organizational, and technical safeguards where required by applicable law.

## 8. Security

We use reasonable administrative and technical safeguards, including encrypted transport, access controls, password hashing, scoped credentials, and security monitoring. No internet service can guarantee absolute security. You are responsible for safeguarding your account credentials and API keys and should notify us promptly of suspected misuse.

## 9. Children

Lazu is intended for adults and business users and is not directed to children under 13. If you believe a child has provided personal information without appropriate authorization, contact us so we can review and delete it where required.

## 10. Changes to This Policy

We may update this policy as the service, providers, or legal requirements change. We will publish the current version here and provide additional notice when a change is material. The date above identifies the latest revision.

## 11. Contact Us

For privacy questions, rights requests, or complaints, contact:

**DUBRIO LLC**
Delaware, United States
Email: `support@lazu.ai`
