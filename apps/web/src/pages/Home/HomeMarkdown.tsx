/* The custom-home-page renderer, kept out of the entry bundle.
 *
 * Only a deployment that has replaced its landing page with Markdown ever
 * needs this, so remark/micromark load when that content exists rather than
 * on every first visit. */
import ReactMarkdown from 'react-markdown';
import RemarkGfm from 'remark-gfm';

export default function HomeMarkdown({ content }: { content: string }) {
  return <ReactMarkdown remarkPlugins={[RemarkGfm]}>{content}</ReactMarkdown>;
}
