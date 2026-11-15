import sys
import time
import threading
from concurrent.futures import ThreadPoolExecutor

from config import Config
from logger import log
from simbrief_api import fetch_flight_plan, extract_plan_info
from folder_ops import (
    clear_community_junctions,
    find_matching_addons,
    link_addons,
    is_msfs_running,
)


class SimBriefDaemon:
    def __init__(self):
        self.config = Config()
        self._running = False
        self._thread = None
        self._stop_event = threading.Event()
        self.last_plan_info = None
        self._msfs_was_running = False
        self._poll_timeout = 120

    def _is_new_plan(self, plan_info):
        if not plan_info:
            return False

        last_id = self.config.get("last_processed_plan_id")
        last_time = self.config.get("last_processed_timestamp")

        current_id = plan_info.get("ofp_id")
        current_time = plan_info.get("timestamp")

        if current_id and last_id is not None:
            if current_id != last_id:
                return True
            if current_time and last_time is not None:
                return current_time != last_time
            return False

        if current_time and last_time is not None:
            return current_time != last_time

        if self.last_plan_info:
            return (
                plan_info.get("orig") != self.last_plan_info.get("orig")
                or plan_info.get("dest") != self.last_plan_info.get("dest")
                or plan_info.get("aircraft") != self.last_plan_info.get("aircraft")
                or plan_info.get("airline") != self.last_plan_info.get("airline")
            )

        return True

    def _get_all_library_paths(self):
        count = self.config.get("addon_library_count", 1)
        try:
            count = int(count)
        except (ValueError, TypeError):
            count = 1
        paths = []
        for i in range(1, count + 1):
            key = "addon_library_path" if i == 1 else f"addon_library_path_{i}"
            path = self.config.get(key)
            if path:
                paths.append(path)
        return paths

    def _process_plan(self, plan_info):
        if is_msfs_running():
            if not self._msfs_was_running:
                log("PAUSE", "MSFS is running, skipping link cycle")
                self._msfs_was_running = True
            return

        log("NEW PLAN", f"{plan_info['orig']} -> {plan_info['dest']} | {plan_info['aircraft']} | {plan_info['airline']}")

        library_paths = self._get_all_library_paths()
        matched = []
        for lib_path in library_paths:
            found = find_matching_addons(
                lib_path,
                plan_info.get("orig", ""),
                plan_info.get("dest", ""),
                plan_info.get("aircraft", ""),
                plan_info.get("airline", ""),
            )
            matched.extend(found)

        if not matched:
            log("INFO", "No matching addons found")
            self._mark_processed(plan_info)
            return

        log("INFO", f"Matched {len(matched)} addons")

        if not clear_community_junctions(
            self.config.get("community_folder_path"),
            library_paths
        ):
            log("ERROR", "Failed to clean Community folder")
            return

        linked = link_addons(self.config.get("community_folder_path"), matched)
        log("INFO", f"Successfully linked {linked} addons")

        if linked < len(matched):
            log("WARNING", f"Partially linked addons ({linked}/{len(matched)}); will retry next poll")
            return

        self._mark_processed(plan_info)

    def _mark_processed(self, plan_info):
        self.config.set("last_processed_plan_id", plan_info.get("ofp_id"))
        self.config.set("last_processed_timestamp", plan_info.get("timestamp"))
        self.last_plan_info = plan_info

    def poll_cycle(self):
        if is_msfs_running():
            if not self._msfs_was_running:
                log("PAUSE", "MSFS detected, polling paused")
                self._msfs_was_running = True
            return
        else:
            if self._msfs_was_running:
                log("RESUME", "MSFS closed, resuming operation")
                self._msfs_was_running = False

        user_id = self.config.get("simbrief_user_id")
        log("POLL", f"Querying SimBrief for user {user_id}")

        data = fetch_flight_plan(user_id)
        if data is None:
            return

        plan_info = extract_plan_info(data)
        if not plan_info:
            log("ERROR", "Could not extract plan info")
            return

        if self._is_new_plan(plan_info):
            self._process_plan(plan_info)
        else:
            log("POLL", "No new plan detected")

    def _run_poll_with_timeout(self):
        with ThreadPoolExecutor(max_workers=1) as pool:
            future = pool.submit(self.poll_cycle)
            try:
                future.result(timeout=self._poll_timeout)
            except Exception as e:
                log("ERROR", f"Poll cycle failed or timed out: {e}")

    def _daemon_loop(self):
        log("INFO", "Daemon started")
        try:
            self._run_poll_with_timeout()
        except Exception as e:
            log("ERROR", f"Initial poll failed: {e}")

        while self._running and not self._stop_event.is_set():
            interval = max(10, self.config.get("poll_interval_seconds", 60))

            if sys.stdout is not None:
                try:
                    sys.stdout.write(f"\rWaiting {interval}s for next poll...   ")
                    sys.stdout.flush()
                except Exception:
                    pass

            if self._stop_event.wait(timeout=interval):
                break

            if sys.stdout is not None:
                try:
                    sys.stdout.write("\r" + " " * 40 + "\r")
                    sys.stdout.flush()
                except Exception:
                    pass

            self._run_poll_with_timeout()

    def start(self):
        self._running = True
        self._stop_event.clear()
        self._thread = threading.Thread(target=self._daemon_loop, daemon=True)
        self._thread.start()

    def stop(self):
        self._running = False
        self._stop_event.set()
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=10)
            if self._thread.is_alive():
                log("WARNING", "Daemon thread did not stop within 10 seconds")

    def force_link(self):
        """Manually fetch the latest SimBrief plan and link matching addons."""
        user_id = self.config.get("simbrief_user_id")
        log("POLL", f"Manual link triggered for user {user_id}")
        data = fetch_flight_plan(user_id)
        if data is None:
            log("ERROR", "Manual link: Could not fetch flight plan")
            return
        plan_info = extract_plan_info(data)
        if not plan_info:
            log("ERROR", "Manual link: Could not extract plan info")
            return
        self._process_plan(plan_info)

    def force_unlink(self):
        """Remove only the junctions that sblink created in the Community folder."""
        library_paths = self._get_all_library_paths()
        community = self.config.get("community_folder_path")
        log("INFO", "Manual unlink triggered")
        clear_community_junctions(community, library_paths)

    def run_manual(self):
        self.poll_cycle()
