// Stylesheets and behaviour for the pages the API renders (/models,
// /models/<vendor>/<model> …). They share the marketing pages' global
// CSS and public chrome; scripts/generate-static-pages.mjs writes the tags
// this entry compiles to into dist/__chrome/head.html, which nginx splices in
// through SSI.
import '@fontsource-variable/geist/wght.css';
import '@fontsource/ibm-plex-mono/400.css';
import '@fontsource/ibm-plex-mono/500.css';
import '@fontsource/ibm-plex-mono/600.css';
import './public-site-entry.css';
import { mountPublicChrome } from './chrome';
import { startExtensionPublicSites } from '../extensions/public-site';

mountPublicChrome();
startExtensionPublicSites();
