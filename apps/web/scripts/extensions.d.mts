export interface BuildExtension {
  chromeSections?: Record<string, string>;
  llmsSections?: (env: Record<string, string>) => string;
  sitemaps?: (env: Record<string, string>) => string[];
  checkPrerender?: (context: {
    requireFile: (relative: string, mustInclude?: string[]) => void;
    locale: string;
    prefix: string;
  }) => void;
  devApiPaths?: string[];
}

export function loadBuildExtensions(appRoot: string): Promise<BuildExtension[]>;
