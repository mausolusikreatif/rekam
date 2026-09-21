<!--
  Stands in for a component this edition does not ship. vite.config.js aliases
  the real module to this one when the build's edition lacks the feature, so
  the bundle does not carry UI for routes the server was compiled without.
  Renders nothing; the call sites are gated too (see lib/edition.js), so this
  is belt-and-braces rather than the only guard.
-->
