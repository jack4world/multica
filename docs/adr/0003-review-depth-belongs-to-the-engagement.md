# Review depth belongs to the engagement, and defaults to two

The chain was built with three review levels because three-level review (三级复核) is the accounting firm's rule: 主审 reviews, 项目经理 reviews, 部门负责人 signs. A corporate internal audit department is not a firm. It is commonly five to fifteen people, and a third mandatory signature there means one of two things: a level that is rubber-stamped, or a reviewer invented to fill it. Both are worse than not having the level — a signature nobody reads is a control that reports itself working.

Depth is therefore a property of the engagement (`project.review_levels`, `CHECK BETWEEN 1 AND 3`, default 2), not a global setting and not a property of a workpaper. A department that runs one review for a routine 专项审计 and three for the annual 经济责任审计 configures each engagement once; a workpaper never negotiates its own depth, so two workpapers in the same engagement can never be held to different standards.

Two, not three, is the default: the value most departments will never change should be the one they need.

## Consequences

- `auditgate` is depth-driven. `LevelsUpTo(depth)` truncates the chain, the last level files, and a status beyond the depth is refused by name ("this engagement runs 2 review levels") rather than as a generic illegal transition — a reader who picked `review_l3` needs to know the level does not exist here, not that they stepped out of order.
- The gate's decision inputs must carry the depth. It is read on the write path of every governed transition, by primary key, in the same transaction as the actor's rank and the preparer; reading it from another snapshot would let a depth change race a transition it governs.
- Seating a rank the engagement does not run is refused at the moment it is made. Catching it then costs one 400; catching it later means a workpaper that cannot move and nobody knows why.
- Lowering the depth under a workpaper already at a deeper stage is refused (409, with a count). No transition would reach such a workpaper and none would leave it, so a configuration correction would become a data problem.
- Raising the depth is always allowed. A workpaper mid-chain simply has further to go, which is the outcome the person raising it asked for.
- Depth is not retroactive to what has already been filed. A workpaper filed under two levels stays filed; its trail records the levels it actually passed, which is what an external reviewer asks about.
