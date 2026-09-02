// Activity page: server-backed stats summary + paginated activity table with
// in-flight requests. Ported from routes/Activity.svelte.
import { el, cleanupAll } from "../dom.js";
import { activityRevision, getActivityStats } from "../api.js";
import { connectionState } from "../theme.js";
import { ActivityStats } from "../components/activityStats.js";
import { ActivityTable } from "../components/activityTable.js";

// SSE-driven refreshes are throttled to one per second.
const REFRESH_THROTTLE_MS = 1000;

export function ActivityPage() {
  const stats = ActivityStats();
  const table = ActivityTable({ showModel: true, showPagination: true, storagePrefix: "activity" });

  const root = el(`
    <div class="page page-activity">
      <div class="activity-stats-wrap" data-stats></div>
      <div data-table></div>
    </div>
  `);
  root.querySelector("[data-stats]").appendChild(stats.el);
  root.querySelector("[data-table]").appendChild(table.el);

  let refreshTimer = null;
  let lastRefresh = 0;
  let requestID = 0;

  async function refreshStats() {
    const id = ++requestID;
    try {
      // Stats stay unfiltered: the cards describe all recorded activity, not
      // the current table view.
      const activityStats = await getActivityStats();
      if (id !== requestID) return;
      stats.setStats(activityStats);
    } catch (error) {
      console.error("Failed to refresh activity stats:", error);
    }
  }

  function scheduleRefresh() {
    if (refreshTimer !== null) return;
    const wait = Math.max(0, REFRESH_THROTTLE_MS - (Date.now() - lastRefresh));
    refreshTimer = setTimeout(() => {
      refreshTimer = null;
      lastRefresh = Date.now();
      table.refresh();
      refreshStats();
    }, wait);
  }

  let seenRevision = activityRevision.get();
  const subs = [
    activityRevision.subscribe((revision) => {
      // New activity only changes what page 1 shows; skip refreshes while the
      // user browses older pages.
      if (revision === seenRevision) return;
      seenRevision = revision;
      scheduleRefresh();
    }),
    connectionState.subscribe((state) => {
      if (state === "connected") {
        table.refresh();
        refreshStats();
      }
    }),
  ];

  refreshStats();

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
      if (refreshTimer !== null) clearTimeout(refreshTimer);
      stats.destroy();
      table.destroy();
    },
  };
}
