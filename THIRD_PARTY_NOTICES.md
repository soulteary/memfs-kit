# Third-party sources and licensing

`memfs-kit` is distributed with Apache License 2.0 as its project license.
Original code inherited from `memfs` retains its BSD-3-Clause licensing,
copyright notice, conditions, and disclaimer. Apache-2.0 applies to the
original contributions made to this project; it does not remove the upstream
BSD obligations or grant patent rights on behalf of upstream authors.

## memfs

- Original project: <https://github.com/psanford/memfs>
- Migration source: <https://github.com/soulteary/memfs>
- Migration source revision: `b8d5ff9859d1c18004594f555725fb241563c1a0`
- Reviewed upstream revision: `4ef911798f9bffd5efa9346abd6e81ff3896d114`
- Copyright: Copyright (c) 2021 The memfs Authors. All rights reserved.
- License: [BSD-3-Clause, original text](LICENSES/memfs-BSD-3-Clause.txt)

The migration source includes the `fstest` compatibility work credited to
[@cugu](https://github.com/cugu) in its README and imported from
[psanford/memfs#3](https://github.com/psanford/memfs/pull/3). The upstream
history also contains contributions by its original author and other
contributors. These acknowledgements supplement the preserved copyright
notice; they do not replace it.

The imported implementation and tests have been revised for this project.
The review of upstream commits and migration decisions is recorded in
[docs/UPSTREAM_REVIEW.md](docs/UPSTREAM_REVIEW.md).

Keep the complete original BSD license with source redistributions. For
binary redistributions, reproduce its copyright notice, conditions, and
disclaimer in the accompanying documentation or other distribution materials,
as required by that license.
