"use client";

import { use } from "react";
import { AuditReportPage } from "@multica/views/audit";

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  return <AuditReportPage projectId={id} />;
}
