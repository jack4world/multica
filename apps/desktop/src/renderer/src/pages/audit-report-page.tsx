import { useParams } from "react-router-dom";
import { AuditReportPage as AuditReportView } from "@multica/views/audit";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function DesktopAuditReportPage() {
  const { id } = useParams<{ id: string }>();
  useDocumentTitle("Audit Report");
  if (!id) return null;
  return <AuditReportView projectId={id} />;
}
