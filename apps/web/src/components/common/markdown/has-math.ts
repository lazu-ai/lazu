/* Whether content may hold a formula KaTeX renders: `$` delimiters, or a
   fenced math block (```math), which rehype-katex renders as display math. */
const MATH_FENCE = /^[ \t]*(`{3,}|~{3,})[ \t]*math\b/m;

export function hasMath(content: string): boolean {
  return content.includes('$') || MATH_FENCE.test(content);
}
