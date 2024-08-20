import sys
import os
import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from dotenv import load_dotenv
from ..main import app

# Add the parent directory of 'main.py' to sys.path
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '..')))

# Get the DATABASE_URL from the environment variables
DATABASE_URL = os.getenv("DATABASE_URL")

client = TestClient(app)

def test_read_root():
    response = client.get("/")
    assert response.status_code == 200
    assert response.json() == {"message": "Welcome to Fourseas Credit Card Command Center"}

def test_database_connection():
    """Test the database connection."""
    # Create the SQLAlchemy engine
    engine = create_engine(DATABASE_URL, connect_args={"connect_timeout": 10})
    
    try:
        # Try to connect and execute a simple query
        with engine.connect() as connection:
            result = connection.execute(text("SELECT 1")).fetchone()
        
        # Assert that the connection was successful and returned the expected result
        assert result == (1,), f"Expected (1,), but got {result}"
        print("Database connection successful!")
    except Exception as e:
        pytest.fail(f"Database connection failed: {str(e)}")

# def test_register_user():
#     user_data = {
#         "email": "test@example.com",
#         "first_name": "Test",
#         "last_name": "User",
#         "password": "testpassword"
#     }
#     response = client.post("/users/register", json=user_data)
#     assert response.status_code == 200
#     data = response.json()
#     assert data["email"] == user_data["email"]
#     assert data["first_name"] == user_data["first_name"]
#     assert data["last_name"] == user_data["last_name"]
#     assert "id" in data
#     assert "created_at" in data
#     assert "updated_at" in data

def test_login():
    user_data = {
        "email": "test@example.com",
        "password": "testpassword"
    }
    response = client.post("/users/login", json=user_data)
    assert response.status_code == 200
    data = response.json()
    assert "access_token" in data
    assert data["token_type"] == "bearer"