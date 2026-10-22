// The open channel between the places that offer 报告问题 (an error toast,
// the Help menu) and the dialog. It lives in its own module so the toast
// helper can open the dialog without importing it.

export const REPORT_PROBLEM_OPEN_EVENT = 'lazu:open-report-problem';

export type ReportProblemDetail = {
  /** LZ-#### of the failure the reader saw, if any. */
  error?: string;
  /** The sentence the toast showed. */
  message?: string;
  /** req_… of the failed request, if any. */
  requestId?: string;
};

export const openReportProblem = (detail?: ReportProblemDetail) => {
  window.dispatchEvent(
    new CustomEvent(REPORT_PROBLEM_OPEN_EVENT, { detail: detail ?? {} }),
  );
};
