---
title: The MCP endpoint
taxonomy: docs.agents
---

rekam speaks the Model Context Protocol, so an agent reads and writes your
corpus without any glue written for one provider.

## Connecting

You add rekam as a connector in your assistant. It opens a rekam login, you sign
in with your own email and password, and an authorization is issued for your
identity. There is no shared password and no key to paste — a connection made by
you reaches your memory and nobody else's.

A connection is bound to one workspace. If you belong to more than one, you
choose which during the login; connecting a second means authorizing a second
time, because an existing connection cannot be widened.

## What an agent can do

Browse the taxonomy and drill into a branch. Search, ranked the way
[[How search works]] describes. Read a record in full, with the records that
link to it. Walk the link graph from one record to its neighbours. Write new
records and patch existing ones. Read a record's revisions and roll one back.

Each tool is declared to the client as read-only or destructive, so an assistant
knows which calls to confirm with you before making.

Writes are bounded by what the identity behind the connection is allowed to do —
see [[Authorship and history]].

## Switching providers

The endpoint is the entire integration. Connect a different assistant to the
same account and the corpus is unchanged: same records, same taxonomy, same
graph, same history. That is the reason to keep memory outside the assistant —
see [[What rekam is]].
