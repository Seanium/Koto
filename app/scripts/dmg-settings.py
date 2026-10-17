"""Finder metadata for the installation disk; no Finder preferences are changed."""
from pathlib import Path

application = Path(defines["app"])
files = [str(application)]
symlinks = {"Applications": "/Applications"}
format = "UDZO"
filesystem = "HFS+"
background = defines["background"]
icon = str(application / "Contents" / "Resources" / "icons.icns")
icon_locations = {application.name: (186, 226), "Applications": (474, 226)}
window_rect = ((160, 180), (660, 410))
default_view = "icon-view"
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
arrange_by = None
scroll_position = (0, 0)
show_icon_preview = False
show_item_info = False
include_icon_view_settings = True
include_list_view_settings = False
icon_size = 96
text_size = 14
label_pos = "bottom"
# Do not set FinderInfo on the signed bundle; even hiding its extension would
# invalidate Apple's strict resource verification after copying into the image.
