# Access task intro contract

`AccessTaskIntro` introduces one focused hosted-access task with a localized
purpose label, the page's single `h1`, and bounded supporting copy. It is
proven by password recovery, Invitation acceptance, and provider connection;
the owning feature still decides whether a loading, success, or terminal state
needs the intro.

The component owns only typography and vertical rhythm. It does not own a
route, workflow steps, API state, evidence, navigation, or actions. Content
may expand and wrap without changing order, and the purpose label is never the
only source of the page heading or state meaning.
