package io.github.acip.minimedia.data

import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

interface MiniMediaApi {
    @GET("api/info")
    suspend fun info(): ServerInfo

    @GET("api/home")
    suspend fun home(): HomeResponse

    @GET("api/movies")
    suspend fun movies(@Query("sort") sort: String, @Query("genre") genre: String? = null): List<Media>

    @GET("api/shows")
    suspend fun shows(@Query("sort") sort: String, @Query("genre") genre: String? = null): List<Media>

    @GET("api/shows/{id}")
    suspend fun show(@Path("id") id: String): ShowDetail

    @GET("api/items/{id}")
    suspend fun item(@Path("id") id: String): ItemDetail

    @GET("api/search")
    suspend fun search(@Query("q") q: String): SearchResult

    @POST("api/items/{id}/playback")
    suspend fun playback(@Path("id") id: String, @Body req: PlaybackRequest): PlaybackInfo

    @PUT("api/items/{id}/progress")
    suspend fun progress(@Path("id") id: String, @Body report: ProgressReport): ProgressState

    @POST("api/items/{id}/watched")
    suspend fun markWatched(@Path("id") id: String): ProgressState

    @DELETE("api/items/{id}/watched")
    suspend fun markUnwatched(@Path("id") id: String): ProgressState

    @PUT("api/shows/{id}/prefs")
    suspend fun putPrefs(@Path("id") id: String, @Body prefs: ShowPrefs): ShowPrefs
}
