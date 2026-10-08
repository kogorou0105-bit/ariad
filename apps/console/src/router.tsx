import { createRootRoute, createRoute, createRouter } from "@tanstack/react-router";

import { ConsoleShell, OverviewPage } from "./App";
import { AdministratorsPage } from "./pages/AdministratorsPage";
import { ConversationDetailPage, ConversationsPage, VisitorsPage } from "./pages/ConversationPages";
import { KnowledgeSourceDetailPage, KnowledgeSourcesPage } from "./pages/KnowledgePages";
import { PlaygroundPage } from "./pages/PlaygroundPage";
import { ModelConfigPage } from "./pages/ModelConfigPage";
import { ReviewDetailPage, ReviewQueuePage } from "./pages/ReviewPages";
import { EvaluationsPage } from "./pages/EvaluationsPage";

const rootRoute = createRootRoute({ component: ConsoleShell });
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: OverviewPage,
});
const conversationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/conversations",
  validateSearch: (search: Record<string, unknown>) => ({ visitorId: typeof search.visitorId === "string" ? search.visitorId : "" }),
  component: ConversationsPage,
});
const visitorsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/visitors", component: VisitorsPage });
const knowledgeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/knowledge", component: KnowledgeSourcesPage });
const knowledgeDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: "/knowledge/$sourceId", component: KnowledgeSourceDetailPage });
const playgroundRoute = createRoute({ getParentRoute: () => rootRoute, path: "/playground", component: PlaygroundPage });
const conversationDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/conversations/$conversationId",
  validateSearch: (search: Record<string, unknown>) => ({
    visitorId: typeof search.visitorId === "string" ? search.visitorId : "",
  }),
  component: ConversationDetailPage,
});
const modelConfigRoute = createRoute({ getParentRoute: () => rootRoute, path: "/settings/model", component: ModelConfigPage });
const reviewsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/reviews", component: ReviewQueuePage });
const reviewDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: "/reviews/$conversationId", validateSearch: (search: Record<string, unknown>) => ({ visitorId: typeof search.visitorId === "string" ? search.visitorId : "" }), component: ReviewDetailPage });
const administratorsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/settings/administrators", component: AdministratorsPage });
const evaluationsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/evaluations", component: EvaluationsPage });
const routeTree = rootRoute.addChildren([indexRoute, knowledgeRoute, knowledgeDetailRoute, playgroundRoute, evaluationsRoute, conversationsRoute, conversationDetailRoute, visitorsRoute, reviewsRoute, reviewDetailRoute, modelConfigRoute, administratorsRoute]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
