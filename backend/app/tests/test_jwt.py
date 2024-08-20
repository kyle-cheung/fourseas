import pytest
from fastapi.testclient import TestClient
from ..main import app
from ..auth.user_service import UserService
from ..database.schemas import UserLogin
from sqlalchemy.orm import Session
from datetime import timedelta

client = TestClient(app)

@pytest.fixture
def db():
    # This should return a database session
    # You might need to adjust this based on your actual database setup
    from ..database.connection import get_db
    return next(get_db())

def test_login_and_protected_route(db: Session):
    # Login
    login_data = UserLogin(email="test@example.com", password="testpassword")
    token_response = UserService.login(db, login_data)
    token = token_response["access_token"]

    # Access protected route
    headers = {"Authorization": f"Bearer {token}"}
    response = client.get("/users/me", headers=headers)
    assert response.status_code == 200
    assert response.json()["email"] == "test@example.com"

def test_protected_route_without_token():
    response = client.get("/users/me")
    assert response.status_code == 403  # FastAPI's HTTPBearer returns 403 for missing token

def test_protected_route_with_invalid_token():
    headers = {"Authorization": "Bearer invalidtoken"}
    response = client.get("/users/me", headers=headers)
    assert response.status_code == 401

def test_protected_route_with_expired_token(db: Session):
    # Create a token that expires immediately
    token = UserService.create_access_token(
        data={"sub": "testjwt@example.com"},
        expires_delta=timedelta(seconds=-1)
    )
    headers = {"Authorization": f"Bearer {token}"}
    response = client.get("/users/me", headers=headers)
    assert response.status_code == 401