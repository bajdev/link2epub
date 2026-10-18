# Security policy

## Supported versions

Security fixes go into the latest release only. Please update to it before reporting a problem.

## Reporting a vulnerability

Please report security problems privately, not in a public issue: on GitHub, open the repository's **Security** tab and choose **Report a vulnerability**. Include the output of `link2epub -VERSION`, the command you ran, and what happened.

You should get a reply within a week. Once a fix is released, the report is published as a security advisory, with credit to you unless you prefer otherwise.

## Scope

link2epub downloads web pages chosen by its user, which are untrusted input. Problems in scope include:

- a page making link2epub read or write files other than the book it was asked to save;
- a page making link2epub connect to the user's own computer or network when `-URL` is on the internet;
- a page producing a book that runs scripts or loads remote content in a reader;
- crashes or runaway memory use caused by a page or image.

What link2epub does to stay safe is described under [Privacy and security](README.md#privacy-and-security) in the README.
