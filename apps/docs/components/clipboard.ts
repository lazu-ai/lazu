// Clipboard fallback for plain-HTTP self-hosted docs and denied permissions.
export async function writeClipboard(text: string): Promise<void> {
  const legacy = () => {
    const scratch = document.createElement("textarea");
    scratch.value = text;
    scratch.readOnly = true;
    scratch.style.position = "fixed";
    scratch.style.opacity = "0";
    document.body.appendChild(scratch);
    scratch.select();
    try {
      if (!document.execCommand("copy")) throw new Error("copy rejected");
    } finally {
      scratch.remove();
    }
  };
  if (!navigator.clipboard?.writeText) return legacy();
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    legacy();
  }
}
