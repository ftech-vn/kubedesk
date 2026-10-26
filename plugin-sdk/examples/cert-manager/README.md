# cert-manager for Kube Desk

See the certificates of [cert-manager](https://cert-manager.io) in Kube Desk, and the ones that need you before
they expire.

- **Certificates**, a page under Plugins: every certificate of the namespace, or of all, with whether it is ready,
  its DNS names, its issuer and when it expires. **Only problems** keeps those not ready, expired, or expiring soon.
- **A card on the overview** counts the certificates not ready or expiring soon, and opens them.
- **Expires**, a column in the list of Certificates under Custom Resources, and a **cert-manager** section in a
  certificate's details: its readiness, message, expiry and renewal time.
- **Open its secret**, in a certificate's menu.

## Settings

How many days before a certificate expires it shows in orange: 14 unless you choose.

Needs cert-manager's `Certificate` resource (`cert-manager.io/v1`) in the cluster, and Kube Desk 0.8 or later.
