# copy_if_missing.cmake — copies SRC → DST only if DST doesn't already exist.
# Used by the runner's POST_BUILD step to seed user-state files (config.json,
# profiles.json) without ever clobbering the user's live edits on rebuild.
#
# Invoked as: cmake -DSRC=... -DDST=... -P copy_if_missing.cmake

if(NOT DEFINED SRC OR NOT DEFINED DST)
  message(FATAL_ERROR "copy_if_missing.cmake: SRC and DST are required")
endif()

if(EXISTS "${DST}")
  message(STATUS "copy_if_missing: skip (exists) ${DST}")
  return()
endif()

if(NOT EXISTS "${SRC}")
  message(WARNING "copy_if_missing: SRC missing, nothing to seed: ${SRC}")
  return()
endif()

get_filename_component(DST_DIR "${DST}" DIRECTORY)
file(MAKE_DIRECTORY "${DST_DIR}")
configure_file("${SRC}" "${DST}" COPYONLY)
message(STATUS "copy_if_missing: seeded ${DST}")
