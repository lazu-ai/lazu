/** Where the console tells an agent to send its requests. */

/** An address this console is willing to write into a command someone pastes
 *  into their terminal: http(s), a host, an optional port and path, and not a
 *  character that means anything to a shell. It mirrors the rule the backend
 *  applies in agentsetup.NormalizeBaseURL, because the two produce the same
 *  installer line. A deployment that sets PUBLIC_SERVER_ADDRESS to something
 *  else is not trusted with it. */
export const isUsableOrigin = (value: string): boolean => {
  const trimmed = value.trim();
  if (!/^https?:\/\//i.test(trimmed)) return false;
  // Brackets are here for IPv6 literals such as https://[2001:db8::1]:8443.
  if (!/^[A-Za-z0-9.:/_[\]-]+$/.test(trimmed)) return false;
  try {
    const url = new URL(trimmed);
    return !url.search && !url.hash && !url.username && !url.password;
  } catch {
    return false;
  }
};

/** Loopback is a range, not a spelling: 127.0.0.0/8 is sixteen million
 *  addresses and ::1 can be written out in full. */
export const isLoopbackOrigin = (value: string): boolean => {
  let host = '';
  try {
    host = new URL(value).hostname.toLowerCase();
  } catch {
    return false;
  }
  if (host === 'localhost' || host.endsWith('.localhost')) return true;
  if (/^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host)) return true;
  // URL strips the brackets from an IPv6 host and canonicalizes what is
  // inside, so ::ffff:127.0.0.1 arrives as ::ffff:7f00:1. An IPv4-mapped
  // address is loopback exactly when the address it maps is.
  const bare = host.replace(/^\[|\]$/g, '');
  if (!bare.includes(':')) return false;
  const mapped = /^(?:0*:)*0*:?ffff:(.+)$/i.exec(bare);
  if (mapped) {
    const tail = mapped[1];
    if (tail.includes('.')) return /^127\./.test(tail);
    // Two hex groups: the first sixteen bits carry the leading octet.
    const [high] = tail.split(':');
    return parseInt(high, 16) >> 8 === 127;
  }
  const groups = bare.split(':');
  const last = groups.pop() || '';
  return (
    /^0*1$/.test(last) &&
    groups.every((group) => group === '' || /^0+$/.test(group))
  );
};
