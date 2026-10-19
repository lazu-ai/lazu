// Stylesheets and behaviour for the pages the API renders (/models,
// /models/<vendor>/<model> …). They share the marketing pages' global
// CSS and public chrome; scripts/generate-static-pages.mjs writes the tags
// this entry compiles to into dist/__chrome/head.html, which nginx splices in
// through SSI.
import '@fontsource-variable/geist/wght.css';
import '@fontsource/ibm-plex-mono/400.css';
import '@fontsource/ibm-plex-mono/500.css';
import '@fontsource/ibm-plex-mono/600.css';
import '../index.css';
import '../styles/brand.generated.css';
import '../styles/tokens.css';
import '../styles/ui.css';
import '../styles/shell.css';
import '../styles/atmosphere.css';
import '../styles/yunmu.css';
import '../styles/fonts.css';
import '../pages/Home/lazu-landing.css';
import '../components/marketing/public-footer.css';
import './public-site.css';
import '../components/common/DocumentRenderer/legal-document.css';
import { mountPublicChrome } from './chrome';
import { startExtensionPublicSites } from '../extensions/public-site';

mountPublicChrome();
startExtensionPublicSites();
