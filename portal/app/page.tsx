import { Studio } from "@/components/studio";

export default function Page() {
  return <Studio localPreview={process.env.NODE_ENV === "development"} />;
}
