# Pheme documentation

Read the operator documentation in this order:

1. [Deployment](deployment.md) - install a standalone server and place secrets,
   persistent data, TLS, push, mail, and TURN configuration.
2. [Server operation](server-operation.md) - understand processes, data flows,
   storage, backup, scaling, upgrades, and failure behavior.
3. [Protocol and security model](protocol.md) - understand client authentication,
   channels, MLS conversations, calls, and host-to-host transport.
4. [Federation](federation.md) - join a network, operate a nodelist, expose S2S
   endpoints, and understand the hub model.

Historical design notes and contributor-oriented material live in
[`development/`](development/). They explain why parts of the system were built
as they were, but the documents above describe current operator behavior.
