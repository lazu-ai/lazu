/* The first-generation snippets on the empty 生成任务 view: curl and Python,
 * for an image and for a video. Each is a list of pieces so the view can
 * colour strings and comments and put the Key where it goes; the copy button
 * joins them with the full Key. */

export type SnippetKind = 'image' | 'video';
export type SnippetLang = 'curl' | 'python';

/** A run of code; `tone` colours it, `key` marks where the Key goes. */
type SnippetPiece =
  | { text: string; tone?: 'string' | 'comment' }
  | { key: true };

const s = (text: string): SnippetPiece => ({ text, tone: 'string' });
const c = (text: string): SnippetPiece => ({ text, tone: 'comment' });
const p = (text: string): SnippetPiece => ({ text });
const KEY: SnippetPiece = { key: true };

const SNIPPET_MODELS = {
  image: 'seedream-5.0-lite',
  video: 'seedance-2.5',
} as const;

type SnippetCopy = {
  imagePrompt: string;
  videoPrompt: string;
  waitComment: string;
  taskIdLabel: string;
};

export function snippetPieces(
  kind: SnippetKind,
  lang: SnippetLang,
  base: string,
  copy: SnippetCopy,
): SnippetPiece[] {
  if (lang === 'curl' && kind === 'image') {
    return [
      p(`curl ${base}/v1/images/generations \\\n  -H "Authorization: Bearer `),
      KEY,
      p(`" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model": `),
      s(`"${SNIPPET_MODELS.image}"`),
      p(',\n       "prompt": '),
      s(`"${copy.imagePrompt}"`),
      p("}'"),
    ];
  }
  if (lang === 'curl') {
    return [
      p(`curl ${base}/v1/videos \\\n  -H "Authorization: Bearer `),
      KEY,
      p(`" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model": `),
      s(`"${SNIPPET_MODELS.video}"`),
      p(', "prompt": '),
      s(`"${copy.videoPrompt}"`),
      p("}'\n\n"),
      c(`# ${copy.waitComment}`),
      p(
        `\ncurl "${base}/v1/videos/<${copy.taskIdLabel}>?wait=60" \\\n  -H "Authorization: Bearer `,
      ),
      KEY,
      p('"'),
    ];
  }
  if (kind === 'image') {
    return [
      p('import requests\n\nr = requests.post(\n    '),
      s(`"${base}/v1/images/generations"`),
      p(',\n    headers={'),
      s('"Authorization"'),
      p(': '),
      s('"Bearer '),
      KEY,
      s('"'),
      p('},\n    json={'),
      s('"model"'),
      p(': '),
      s(`"${SNIPPET_MODELS.image}"`),
      p(',\n          '),
      s('"prompt"'),
      p(': '),
      s(`"${copy.imagePrompt}"`),
      p('},\n)\nprint(r.json()['),
      s('"data"'),
      p('][0])'),
    ];
  }
  return [
    p('import requests\n\napi = '),
    s(`"${base}/v1/videos"`),
    p('\nh = {'),
    s('"Authorization"'),
    p(': '),
    s('"Bearer '),
    KEY,
    s('"'),
    p('}\njob = requests.post(api, headers=h, json={'),
    s('"model"'),
    p(': '),
    s(`"${SNIPPET_MODELS.video}"`),
    p(',\n    '),
    s('"prompt"'),
    p(': '),
    s(`"${copy.videoPrompt}"`),
    p('}).json()\nwhile job['),
    s('"status"'),
    p('] not in ('),
    s('"succeeded"'),
    p(', '),
    s('"failed"'),
    p('):\n    job = requests.get(f'),
    s(`"{api}/{job['id']}"`),
    p(', headers=h,\n                       params={'),
    s('"wait"'),
    p(': 60}).json()\nprint(job.get('),
    s('"artifact"'),
    p('))'),
  ];
}

/** The snippet as text, with the Key in place. */
export const snippetText = (pieces: SnippetPiece[], key: string) =>
  pieces.map((piece) => ('key' in piece ? key : piece.text)).join('');
