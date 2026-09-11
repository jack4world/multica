"use client";

import { WorkspaceLanding } from "@multica/views/layout";

// A bare `/{slug}` is where a login and the root redirect land. Which page that
// means depends on what kind of workspace it is, and only the client knows.
export default function Page() {
  return <WorkspaceLanding />;
}
