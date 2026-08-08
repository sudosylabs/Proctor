---
status: accepted
---

# Select concrete infrastructure at the composition root

The module-root `server` composition package selects and constructs concrete
persistence, cache, cluster, mail, VFS, and external-authentication adapters.
`platform.New` receives those capabilities and owns their shared lifecycle but
does not choose implementations from configuration. Centralizing selection
makes dependency direction and deployment behavior reviewable, while cohesive
root-package construction files prevent the root constructor from becoming one
undifferentiated function. `platform.Service` remains a bounded owner for
infrastructure health, dynamic reconfiguration, startup, and shutdown; it is
retained by the runtime server but never passed into application services as a
capability locator.
