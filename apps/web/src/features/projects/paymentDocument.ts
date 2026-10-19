export function paymentDocumentPath(
  projectId: string,
  topupId: string,
  document: 'invoice' | 'invoice_page' | 'receipt',
) {
  const params = new URLSearchParams({
    project_id: projectId,
    top_up_id: topupId,
    document,
    source: 'wallet',
  });
  return `/payment/document?${params.toString()}`;
}
