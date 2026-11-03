# Security reporting

Do not disclose live credentials, private keys, PSKs, configuration/state files, client identities or exploitable vulnerability details in a public issue.

No dedicated private security contact or supported-version policy is documented at this revision. If the repository provides GitHub private vulnerability reporting, use that channel. Otherwise, request a private reporting route from the maintainer without publishing exploit details. This document does not promise a response time or security support.

Provide affected commit/version, environment, impact and a minimal reproduction using synthetic credentials through the agreed private channel. For ordinary bugs, use a public issue with sanitized logs.

Operators should restrict HTTPS management access, verify certificate fingerprints, protect root-owned secrets and backups, and review operating-system and dependency updates. Plain L2TP is unencrypted; optional IKEv1/PSK is a legacy mode with a shared secret. HTTPS protects management transport, not the complete data path. See [operations](docs/en/operations.md) and [legal notes](docs/en/legal.md).
