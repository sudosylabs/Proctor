# Loading indicator contract

`Loading` communicates pending work through the semantic `loading` icon from
the owned Icon adapter. Its required localized `label` remains accessible;
`showLabel` places it below the spinner when visible copy is useful. The icon
is decorative, inherits the parent's color, and uses the governed icon sizes.
The component owns no operation, timer, retry, focus, or completion behavior.

The default polite status region announces the label. Consumers that already
own a live region, including Button and TaskState, use `announce={false}` to
keep one announcement path. Loading never adds a focus target. Rotation runs
only with no reduced-motion preference; the stationary icon and status remain
available in reduced motion and forced colors.

The component sizes to its icon and optional wrapping label. It supplies no
viewport dimensions, minimum panel height, backdrop, fixed positioning, or
page padding. A parent allocates and centers the pending area through its
ordinary layout or a local class, and reserves space for its eventual content.
Known page content remains outside that area. Bounds are responsive minimums,
not a claim to know the height of server-dependent content before it arrives;
the resolved content may grow naturally without clipping.

Button reserves its existing content's dimensions and centers Loading over
that footprint. Page consumers reserve a bounded content region and keep
available headings and context visible. Browser coverage proves delayed
loading, completion and retry, intrinsic and constrained parents, button
dimensions, keyboard behavior, expanded labels, themes, zoom, and motion.
