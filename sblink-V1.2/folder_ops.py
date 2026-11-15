import ctypes
import csv
import os
import shutil
import subprocess
from pathlib import Path
from collections import deque

from logger import log
from config import Mappings

FILE_ATTRIBUTE_REPARSE_POINT = 0x0400
SBLINK_MARKER = ".sblink"


def _has_sblink_marker(path):
    try:
        return (Path(path) / SBLINK_MARKER).exists()
    except OSError:
        return False


def _write_sblink_marker(link_path):
    try:
        marker = Path(link_path) / SBLINK_MARKER
        marker.write_text("sblink", encoding="utf-8")
    except OSError:
        pass


def _remove_sblink_marker(link_path):
    try:
        marker = Path(link_path) / SBLINK_MARKER
        if marker.exists():
            marker.unlink()
    except OSError:
        pass


def is_junction_or_symlink(path):
    path = Path(path)

    try:
        if os.path.islink(path):
            return True
    except (OSError, ValueError):
        pass

    try:
        if hasattr(os.path, "isjunction") and os.path.isjunction(path):
            return True
    except (OSError, ValueError):
        pass

    try:
        attrs = ctypes.windll.kernel32.GetFileAttributesW(str(path))
        if attrs != -1:
            return bool(attrs & FILE_ATTRIBUTE_REPARSE_POINT)
    except Exception:
        pass

    return False


def is_msfs_running():
    try:
        flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
        result = subprocess.run(
            ["tasklist", "/fo", "csv", "/nh"],
            capture_output=True,
            text=True,
            shell=False,
            timeout=10,
            check=False,
            creationflags=flags
        )
        if result.returncode != 0:
            return False

        reader = csv.reader(result.stdout.strip().splitlines())
        for row in reader:
            if not row:
                continue
            process_name = row[0].strip().strip('"').lower()
            if "flightsimulator" in process_name:
                return True
        return False
    except (subprocess.TimeoutExpired, OSError, FileNotFoundError):
        return False


def clear_community_junctions(community_path, addon_library_paths):
    community = Path(community_path)
    if not community.exists():
        log("ERROR", f"Community folder does not exist: {community}")
        return False
    if not community.is_dir():
        log("ERROR", f"Community path is not a directory: {community}")
        return False

    if isinstance(addon_library_paths, str):
        addon_library_paths = [addon_library_paths]

    libraries = []
    for lib_path in addon_library_paths:
        if lib_path:
            try:
                libraries.append(Path(lib_path).resolve())
            except OSError:
                log("WARNING", f"Could not resolve library path: {lib_path}")

    if not libraries:
        log("ERROR", "No addon library paths are set")
        return False

    removed = 0
    for item in community.iterdir():
        if not is_junction_or_symlink(item):
            continue

        if not _has_sblink_marker(item):
            log("INFO", f"Skipping non-sblink junction: {item.name}")
            continue

        try:
            _remove_sblink_marker(item)
            os.rmdir(item)
            log("UNLINK", f"Removed junction: {item.name}")
            removed += 1
        except OSError as e:
            log("ERROR", f"Failed to remove junction {item.name}: {e}")

    log("INFO", f"Cleared {removed} junctions")
    return True


def create_junction(link_path, target_path):
    link_path = Path(link_path).resolve()
    target_path = Path(target_path).resolve()

    if is_junction_or_symlink(link_path):
        try:
            _remove_sblink_marker(link_path)
            os.rmdir(link_path)
        except (OSError, PermissionError):
            try:
                os.unlink(link_path)
            except (OSError, PermissionError) as e2:
                log("ERROR", f"Cannot remove existing junction/symlink {link_path.name}: {e2}")
                return False

    if link_path.exists():
        log("ERROR", f"Real item already exists: {link_path.name}")
        return False

    if not target_path.exists():
        log("ERROR", f"Target path does not exist: {target_path}")
        return False

    try:
        link_path.parent.mkdir(parents=True, exist_ok=True)
    except OSError as e:
        log("ERROR", f"Could not ensure parent directory exists for {link_path}: {e}")
        return False

    try:
        flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
        cmd_path = shutil.which("cmd") or r"C:\Windows\System32\cmd.exe"
        result = subprocess.run(
            [cmd_path, "/c", "mklink", "/J", str(link_path), str(target_path)],
            capture_output=True,
            text=True,
            shell=False,
            check=False,
            creationflags=flags
        )
        if result.returncode != 0:
            err = result.stderr.strip() or result.stdout.strip()
            log("ERROR", f"mklink failed for {link_path.name}: {err}")
            return False

        if not is_junction_or_symlink(link_path):
            log("ERROR", f"mklink reported success but junction not created at {link_path.name}")
            return False

        _write_sblink_marker(link_path)
        log("LINK", f"Created junction: {link_path.name} -> {target_path}")
        return True
    except Exception as e:
        log("ERROR", f"Exception creating junction {link_path.name}: {e}")
        return False


def get_aircraft_search_terms(icao):
    if not icao or len(icao) < 3:
        return []

    icao = icao.upper()
    terms = {icao}

    # strip manufacturer letter prefix, e.g. B738 -> 738
    if (icao.startswith("A") or icao.startswith("B")) and icao[1:4].isdigit():
        terms.add(icao[1:])

    # family groupings - B738 matches the whole B737 family, etc
    family_prefix_map = {
        "A20": "A320",
        "A21": "A320",
        "A22": "A220",
        "A30": "A300",
        "A31": "A310",
        "A33": "A330",
        "A34": "A340",
        "A35": "A350",
        "A38": "A380",
        "B73": "B737",
        "B74": "B747",
        "B75": "B757",
        "B76": "B767",
        "B77": "B777",
        "B78": "B787",
        "E17": "E170",
        "E19": "E190",
    }

    family = None
    if len(icao) >= 3:
        family = family_prefix_map.get(icao[:3])
    if not family and icao.startswith("A3") and len(icao) >= 4:
        family = icao[:3] + "0"

    if family:
        terms.add(family)
        if (family.startswith("A") or family.startswith("B")) and family[1:4].isdigit():
            terms.add(family[1:])

    return list(terms)


def find_matching_addons(library_path, orig, dest, aircraft, airline):
    library = Path(library_path)
    if not library.exists() or not library.is_dir():
        log("ERROR", f"Addon library invalid: {library}")
        return []

    # check manual mappings before doing a full search
    mappings = Mappings()
    ac_terms = get_aircraft_search_terms(aircraft)

    mapped_folder = None
    for term in ac_terms:
        mapped_folder = mappings.get(term)
        if mapped_folder:
            log("INFO", f"Manual mapping found for {term}: {mapped_folder}")
            break

    if mapped_folder:
        for item in library.rglob(mapped_folder):
            if item.is_dir():
                log("MATCH", f"Found mapped folder: {item.name}")
                return [item]
        log("WARNING", f"Mapped folder '{mapped_folder}' not found in library, falling back to search")

    criteria = {
        "origin":      [orig] if orig else [],
        "destination": [dest] if dest else [],
        "aircraft":    ac_terms,
        "airline":     [airline] if airline else [],
    }

    results = {cat: [] for cat in criteria}
    min_depths = {cat: float("inf") for cat in criteria}

    queue = deque([(library, 0)])

    log("INFO", f"Searching in {library} (BFS)")

    while queue:
        folder, depth = queue.popleft()

        if depth > 5:
            continue

        try:
            children = [c for c in folder.iterdir() if c.is_dir()]
        except OSError:
            continue

        for child in children:
            child_name_upper = child.name.upper()

            for cat, values in criteria.items():
                # skip categories where we already have shallower matches
                if depth > min_depths[cat]:
                    continue

                for val in values:
                    if not val or len(val) < 3:
                        continue

                    if val.upper() in child_name_upper:
                        results[cat].append(child)
                        min_depths[cat] = depth
                        log("MATCH", f"{child.name} matched {cat} '{val}' at depth {depth}")
                        break

            # always recurse so we can find liveries inside an aircraft folder
            queue.append((child, depth + 1))

    final_matches = set()
    for cat in criteria:
        for match in results[cat]:
            final_matches.add(match)

    log("INFO", f"Found {len(final_matches)} unique matching addons")
    return list(final_matches)


def link_addons(community_path, matched_addons):
    community = Path(community_path)
    success_count = 0
    seen_names = set()

    for addon in matched_addons:
        link_name = addon.name
        if link_name in seen_names:
            continue
        seen_names.add(link_name)

        link_path = community / link_name
        if create_junction(link_path, addon):
            success_count += 1

    return success_count


def unlink_sblink_junctions(community_path, addon_library_paths):
    return clear_community_junctions(community_path, addon_library_paths)
