cmake_minimum_required(VERSION 3.18.1)

add_library(launcher SHARED launcher.c)

find_library(log-lib log)

target_link_libraries(launcher ${log-lib})

target_link_options(launcher PRIVATE "-Wl,-z,max-page-size=16384")
