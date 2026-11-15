import json
import re
import time
from logger import log

try:
    import requests
    HAS_REQUESTS = True
except ImportError:
    HAS_REQUESTS = False

SIMBRIEF_URL = "https://www.simbrief.com/api/xml.fetcher.php?userid={}&json=1"


def fetch_flight_plan(user_id):
    if not HAS_REQUESTS:
        log("ERROR", "requests library is required. Please install it (pip install requests)")
        return None

    if not user_id or not isinstance(user_id, str):
        log("ERROR", "Invalid SimBrief user ID: must be a non-empty string")
        return None

    user_id = user_id.strip()
    if not re.match(r"^[A-Za-z0-9]+$", user_id):
        log("ERROR", f"Invalid SimBrief user ID: {user_id}")
        return None

    url = SIMBRIEF_URL.format(user_id)
    for attempt in range(3):
        try:
            response = requests.get(url, timeout=30)
            response.raise_for_status()
            data = response.json()
            return data
        except requests.exceptions.Timeout:
            if attempt < 2:
                time.sleep(2 ** attempt)
                continue
            log("ERROR", "Request timed out")
            return None
        except requests.exceptions.ConnectionError:
            if attempt < 2:
                time.sleep(2 ** attempt)
                continue
            log("ERROR", "Connection failed")
            return None
        except requests.exceptions.HTTPError as e:
            log("ERROR", f"HTTP error: {e}")
            return None
        except requests.exceptions.RequestException as e:
            log("ERROR", f"Request failed: {e}")
            return None
        except json.JSONDecodeError:
            log("ERROR", "Failed to parse JSON")
            return None

    return None


def extract_plan_info(data):
    if not data or not isinstance(data, dict):
        return None

    params = data.get("params")
    if params is not None and not isinstance(params, dict):
        log("WARNING", "Unexpected 'params' structure in SimBrief response")
        params = {}
    params = params or {}

    origin = data.get("origin")
    if origin is not None and not isinstance(origin, dict):
        log("WARNING", "Unexpected 'origin' structure in SimBrief response")
        origin = {}
    origin = origin or {}

    destination = data.get("destination")
    if destination is not None and not isinstance(destination, dict):
        log("WARNING", "Unexpected 'destination' structure in SimBrief response")
        destination = {}
    destination = destination or {}

    aircraft = data.get("aircraft")
    if aircraft is not None and not isinstance(aircraft, dict):
        log("WARNING", "Unexpected 'aircraft' structure in SimBrief response")
        aircraft = {}
    aircraft = aircraft or {}

    general = data.get("general")
    if general is not None and not isinstance(general, dict):
        log("WARNING", "Unexpected 'general' structure in SimBrief response")
        general = {}
    general = general or {}

    ofp_id = (
        params.get("OFPID")
        or data.get("OFPID")
        or data.get("ofp_id")
        or params.get("ofp_id")
    )
    timestamp = (
        params.get("TIME_GENERATED")
        or data.get("time_generated")
        or params.get("time_generated")
        or data.get("generated")
    )

    orig = origin.get("icao_code") or data.get("orig") or ""
    dest = destination.get("icao_code") or data.get("dest") or ""
    acft = aircraft.get("icaocode") or data.get("aircraft") or ""
    airline = general.get("icao_airline") or data.get("airline") or ""

    return {
        "ofp_id":    str(ofp_id).strip() if ofp_id else None,
        "timestamp": str(timestamp).strip() if timestamp else None,
        "orig":      orig.upper().strip(),
        "dest":      dest.upper().strip(),
        "aircraft":  acft.upper().strip(),
        "airline":   airline.upper().strip(),
    }
