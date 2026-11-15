import json
import os
from pathlib import Path

from logger import log

CONFIG_FILENAME = "sblink_config.json"

DEFAULTS = {
    "community_folder_path": "",
    "addon_library_path": "",
    "simbrief_user_id": "",
    "poll_interval_seconds": 60,
    "last_processed_plan_id": None,
    "last_processed_timestamp": None,
    "run_hidden": "y",
}


class Config:
    def __init__(self):
        self._path = Path(__file__).parent / CONFIG_FILENAME
        self._data = {}
        self.load()

    def load(self):
        if self._path.exists():
            try:
                with open(self._path, "r", encoding="utf-8") as f:
                    self._data = json.load(f)
            except (json.JSONDecodeError, OSError) as e:
                log("ERROR", f"Failed to load config: {e}")
                self._data = dict(DEFAULTS)
        else:
            self._data = dict(DEFAULTS)
        for key, value in DEFAULTS.items():
            if key not in self._data:
                self._data[key] = value

    def save(self):
        try:
            temp_path = self._path.with_suffix(".tmp")
            with open(temp_path, "w", encoding="utf-8") as f:
                json.dump(self._data, f, indent=4, ensure_ascii=False)
            temp_path.replace(self._path)
        except OSError as e:
            log("ERROR", f"Failed to save config: {e}")

    def get(self, key, default=None):
        return self._data.get(key, default)

    def set(self, key, value):
        self._data[key] = value
        self.save()

    def is_valid(self):
        count = self._data.get("addon_library_count", 1)
        try:
            count = int(count)
        except (ValueError, TypeError):
            count = 1
        if count < 1:
            count = 1
        has_lib = False
        for i in range(1, count + 1):
            key = "addon_library_path" if i == 1 else f"addon_library_path_{i}"
            val = self._data.get(key)
            if val and isinstance(val, str) and val.strip():
                has_lib = True
                break

        return bool(
            self._data.get("community_folder_path")
            and has_lib
            and self._data.get("simbrief_user_id")
        )

    def to_dict(self):
        return dict(self._data)


class Mappings:
    def __init__(self):
        self._path = Path(__file__).parent / "mappings.json"
        self._data = {}
        self.is_new = False
        self.load()

    def load(self):
        if self._path.exists():
            try:
                with open(self._path, "r", encoding="utf-8") as f:
                    self._data = json.load(f)
            except (json.JSONDecodeError, OSError) as e:
                log("ERROR", f"Failed to load mappings: {e}")
                self.is_new = True
                self._create_default()
        else:
            self.is_new = True
            self._create_default()

    def _create_default(self):
        self._data = {
            "example1-ICAOcodeHere": "example1-AircraftOrSceneryFolderNameHere",
            "example2-ICAOcodeHere": "example2-AircraftOrSceneryFolderNameHere",
            "example3-ICAOcodeHere": "example3-AircraftOrSceneryFolderNameHere"
        }
        self.save()

    def save(self):
        try:
            with open(self._path, "w", encoding="utf-8") as f:
                json.dump(self._data, f, indent=4, ensure_ascii=False)
        except OSError as e:
            log("ERROR", f"Failed to save mappings: {e}")

    def get(self, aircraft_icao):
        # normalize to upper for consistent lookups
        return self._data.get(aircraft_icao.upper())

    def set(self, aircraft_icao, folder_name):
        self._data[aircraft_icao.upper()] = folder_name
        self.save()

    def to_dict(self):
        return dict(self._data)
